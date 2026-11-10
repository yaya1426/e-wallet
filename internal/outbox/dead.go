package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/outbox/internal/sqlcgen"
)

// ErrNotDead means the event is not DEAD (for example another reconciliation run resolved it).
var ErrNotDead = errors.New("outbox: event is not dead")

// resolvedPrefix starts last_error of an event resolved by reconciliation.
const resolvedPrefix = "resolved by reconciliation: "

// DeadEvent is an event whose delivery gave up (TDR-006 §3), for reconciliation (TDR-009).
type DeadEvent struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	Type        string
	LastError   string
	Attempts    int
	CreatedAt   time.Time
}

// ListDead returns up to limit DEAD events of the given types, oldest first. q is a pgx.Tx
// or the pool.
func (s *Service) ListDead(ctx context.Context, q Querier, types []string, limit int) ([]DeadEvent, error) {
	rows, err := queries(q).ListDead(ctx, sqlcgen.ListDeadParams{EventTypes: types, RowLimit: toInt32(limit)})
	if err != nil {
		return nil, fmt.Errorf("outbox: list dead: %w", err)
	}
	out := make([]DeadEvent, len(rows))
	for i, r := range rows {
		out[i] = DeadEvent{ID: r.ID, AggregateID: r.AggregateID, Type: r.EventType, LastError: r.LastError,
			Attempts: int(r.Attempts), CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// Resolve finishes a DEAD event that reconciliation handled: DONE, with "resolved by
// reconciliation: " + note in last_error. Call it in the same transaction as the business
// change, so one never happens without the other. A non-DEAD event returns ErrNotDead and
// changes nothing.
func (s *Service) Resolve(ctx context.Context, q Querier, eventID uuid.UUID, note string) error {
	n, err := queries(q).ResolveDead(ctx, sqlcgen.ResolveDeadParams{Now: s.clock.Now(), Note: resolvedPrefix + note, ID: eventID})
	if err != nil {
		return fmt.Errorf("outbox: resolve %s: %w", eventID, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotDead, eventID)
	}
	return nil
}

// Counts are the unfinished and the DEAD events.
type Counts struct {
	Pending int64 // PENDING or PROCESSING
	Dead    int64
}

// Counts counts the outbox events still to deliver and the DEAD ones (ops health, T028).
func (s *Service) Counts(ctx context.Context) (Counts, error) {
	row, err := queries(s.db).Counts(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("outbox: counts: %w", err)
	}
	return Counts{Pending: row.Pending, Dead: row.Dead}, nil
}
