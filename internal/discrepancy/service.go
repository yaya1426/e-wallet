package discrepancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yaya1426/e-wallet/internal/discrepancy/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/id"
)

// Service records and lists discrepancies.
type Service struct {
	db    Querier // the pool, for List
	clock clock.Clock
	log   *slog.Logger
}

// NewService returns the discrepancy service. db is the pool used by List. log may be nil.
func NewService(db Querier, clk clock.Clock, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{db: db, clock: clk, log: log}
}

// Record stores d as OPEN in tx, the caller's unit of work, so it commits together with the
// rest of the caller's changes (TDR-016). While an OPEN discrepancy of the same kind exists
// for the same transaction, recording it again changes nothing.
func (s *Service) Record(ctx context.Context, tx pgx.Tx, d New) error {
	if !d.Kind.valid() || !d.Severity.valid() {
		return fmt.Errorf("%w: kind %q, severity %q", ErrInvalid, d.Kind, d.Severity)
	}
	details, err := json.Marshal(d.Details)
	if err != nil {
		return fmt.Errorf("discrepancy: encode details: %w", err)
	}
	if string(details) == "null" {
		details = []byte("{}")
	}
	n, err := sqlcgen.New(tx).InsertDiscrepancy(ctx, sqlcgen.InsertDiscrepancyParams{
		ID: id.New(), Kind: string(d.Kind), Severity: string(d.Severity),
		TransactionID: uuid.NullUUID{UUID: d.TransactionID, Valid: d.TransactionID != uuid.Nil},
		Details:       details, Now: s.clock.Now(),
	})
	if err != nil {
		return fmt.Errorf("discrepancy: record %s: %w", d.Kind, err)
	}
	attrs := []any{slog.String("kind", string(d.Kind)), slog.String("severity", string(d.Severity)),
		slog.Bool("already_open", n == 0)}
	if d.TransactionID != uuid.Nil {
		attrs = append(attrs, slog.String("transaction_id", d.TransactionID.String()))
	}
	// An operator must act on it; it is only visible once tx commits.
	s.log.WarnContext(ctx, "discrepancy recorded", attrs...)
	return nil
}

// List returns discrepancies matching f, newest first.
func (s *Service) List(ctx context.Context, f Filter) ([]Discrepancy, error) {
	if f.Limit == 0 {
		f.Limit = DefaultListLimit
	}
	if f.Limit < 1 || f.Limit > MaxListLimit || (f.Status != "" && f.Status != StatusOpen && f.Status != StatusResolved) ||
		(f.Kind != "" && !f.Kind.valid()) {
		return nil, fmt.Errorf("%w: filter %+v", ErrInvalid, f)
	}
	params := sqlcgen.ListDiscrepanciesParams{RowLimit: int32(f.Limit)} // bounded by MaxListLimit above
	if f.Status != "" {
		v := string(f.Status)
		params.Status = &v
	}
	if f.Kind != "" {
		v := string(f.Kind)
		params.Kind = &v
	}
	if f.After != nil {
		params.HasCursor, params.CursorTime, params.CursorID = true, f.After.CreatedAt, f.After.ID
	}
	rows, err := sqlcgen.New(s.db).ListDiscrepancies(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("discrepancy: list: %w", err)
	}
	out := make([]Discrepancy, len(rows))
	for i, r := range rows {
		out[i] = fromRow(r)
	}
	return out, nil
}

// Get returns one discrepancy.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Discrepancy, error) {
	row, err := sqlcgen.New(s.db).GetDiscrepancy(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Discrepancy{}, ErrNotFound
	}
	if err != nil {
		return Discrepancy{}, fmt.Errorf("discrepancy: get: %w", err)
	}
	return fromRow(row), nil
}

// Resolve closes an OPEN discrepancy with the operator's note (TDR-009 §3). It never changes
// money; any correction is a separate ledger entry. A resolved one returns ErrAlreadyResolved,
// an unknown one ErrNotFound. Once resolved, the same problem can be recorded again.
func (s *Service) Resolve(ctx context.Context, id uuid.UUID, note string) (Discrepancy, error) {
	row, err := sqlcgen.New(s.db).ResolveDiscrepancy(ctx, sqlcgen.ResolveDiscrepancyParams{Now: s.clock.Now(), Note: note, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := s.Get(ctx, id); getErr != nil {
			return Discrepancy{}, getErr
		}
		return Discrepancy{}, ErrAlreadyResolved
	}
	if err != nil {
		return Discrepancy{}, fmt.Errorf("discrepancy: resolve: %w", err)
	}
	s.log.InfoContext(ctx, "discrepancy resolved", slog.String("discrepancy_id", id.String()), slog.String("kind", row.Kind))
	return fromRow(row), nil
}

func fromRow(r sqlcgen.ReconciliationDiscrepancy) Discrepancy {
	d := Discrepancy{
		ID: r.ID, Kind: Kind(r.Kind), Severity: Severity(r.Severity), Details: json.RawMessage(r.Details),
		Status: Status(r.Status), CreatedAt: r.CreatedAt, ResolvedAt: r.ResolvedAt,
	}
	if r.TransactionID.Valid {
		d.TransactionID = r.TransactionID.UUID
	}
	if r.ResolutionNote != nil {
		d.ResolutionNote = *r.ResolutionNote
	}
	return d
}
