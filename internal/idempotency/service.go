package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/yaya1426/e-wallet/internal/idempotency/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
)

// Key statuses.
const (
	statusInProgress = "IN_PROGRESS"
	statusCompleted  = "COMPLETED"
)

// codeLockNotAvailable is SQLSTATE 55P03, raised when the key insert waits past lock_timeout.
const codeLockNotAvailable = "55P03"

// Service stores idempotency keys.
type Service struct {
	clock clock.Clock
	ttl   time.Duration
}

// NewService returns the service. Keys live for ttl (IDEMPOTENCY_TTL, TDR-005: 24 h).
func NewService(clk clock.Clock, ttl time.Duration) *Service {
	return &Service{clock: clk, ttl: ttl}
}

// RequestHash is the SHA-256 of the decoded, validated request encoded with encoding/json:
// struct fields in declaration order, no whitespace, so the client's key order and spacing
// do not matter.
func RequestHash(v any) ([]byte, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("idempotency: hash request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}

// Begin claims the key inside tx (TDR-005 layer 1). It returns:
//   - nil, nil: the key is new; do the work, then Complete in the same tx;
//   - a *Replay: the same request already completed; answer it as is (WriteReplay);
//   - ErrKeyReused: the key belongs to a different request;
//   - ErrRequestInProgress: another request with the key is still running (including a wait
//     on its uncommitted insert that hit lock_timeout); the tx is then aborted.
//
// An expired row that cleanup has not removed yet counts as absent.
func (s *Service) Begin(ctx context.Context, tx pgx.Tx, req Request) (*Replay, error) {
	q := queries(tx)
	now := s.clock.Now().Truncate(time.Microsecond)
	insert := func() (bool, error) {
		n, err := q.InsertKey(ctx, sqlcgen.InsertKeyParams{
			UserID: req.UserID, Key: req.Key, Method: req.Method, Path: req.Path,
			RequestHash: req.Hash, Now: now, ExpiresAt: now.Add(s.ttl),
		})
		if isLockTimeout(err) {
			return false, ErrRequestInProgress // not wrapped: InTx must not retry (TDR-017 §2)
		}
		if err != nil {
			return false, fmt.Errorf("idempotency: insert key: %w", err)
		}
		return n == 1, nil
	}

	read := func() (sqlcgen.IdempotencyKey, error) {
		row, err := q.GetKey(ctx, sqlcgen.GetKeyParams{UserID: req.UserID, Key: req.Key})
		if err != nil {
			return row, fmt.Errorf("idempotency: read key: %w", err)
		}
		return row, nil
	}

	inserted, err := insert()
	if err != nil || inserted {
		return nil, err
	}
	row, err := read()
	if err != nil {
		return nil, err
	}
	if !row.ExpiresAt.After(now) {
		if _, err := q.DeleteExpiredKey(ctx, sqlcgen.DeleteExpiredKeyParams{UserID: req.UserID, Key: req.Key, Now: now}); err != nil {
			return nil, fmt.Errorf("idempotency: replace expired key: %w", err)
		}
		if inserted, err = insert(); err != nil || inserted {
			return nil, err
		}
		// Another request re-claimed the expired key first and committed: treat its row
		// like any existing key (replay, reused or in progress).
		if row, err = read(); err != nil {
			return nil, err
		}
	}
	if !bytes.Equal(row.RequestHash, req.Hash) || row.Method != req.Method || row.Path != req.Path {
		return nil, ErrKeyReused
	}
	if row.Status != statusCompleted || !row.ResponseStatus.Valid {
		return nil, ErrRequestInProgress
	}
	return &Replay{Status: int(row.ResponseStatus.Int32), Body: row.ResponseBody}, nil
}

// Complete stores the response of the request begun with Begin in the same tx: a success or
// a 4xx business error, so the same key answers the same way (TDR-005 step 3). txnID links
// the transaction the request created, if any.
func (s *Service) Complete(ctx context.Context, tx pgx.Tx, userID uuid.UUID, key string, status int, body any, txnID *uuid.UUID) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("idempotency: encode response: %w", err)
	}
	transactionID := uuid.NullUUID{}
	if txnID != nil {
		transactionID = uuid.NullUUID{UUID: *txnID, Valid: true}
	}
	n, err := queries(tx).CompleteKey(ctx, sqlcgen.CompleteKeyParams{
		ResponseStatus: pgtype.Int4{Int32: int32(status), Valid: true}, //nolint:gosec // HTTP status codes fit in int32
		ResponseBody:   encoded,
		TransactionID:  transactionID,
		UserID:         userID,
		Key:            key,
	})
	if err != nil {
		return fmt.Errorf("idempotency: complete key: %w", err)
	}
	if n != 1 {
		return ErrNotBegun
	}
	return nil
}

// DeleteExpired deletes keys that expired before the clock's now, in batches of batchSize,
// until none are left. It returns how many it deleted.
func (s *Service) DeleteExpired(ctx context.Context, q Querier, batchSize int32) (int64, error) {
	now := s.clock.Now()
	var total int64
	for {
		n, err := queries(q).DeleteExpiredBatch(ctx, sqlcgen.DeleteExpiredBatchParams{Now: now, BatchSize: batchSize})
		if err != nil {
			return total, fmt.Errorf("idempotency: delete expired keys: %w", err)
		}
		total += n
		if n < int64(batchSize) {
			return total, nil
		}
	}
}

func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == codeLockNotAvailable
}
