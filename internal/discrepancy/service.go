package discrepancy

import (
	"context"
	"encoding/json"
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
	if f.Limit < 1 || f.Limit > MaxListLimit || (f.Status != "" && f.Status != StatusOpen && f.Status != StatusResolved) {
		return nil, fmt.Errorf("%w: filter %+v", ErrInvalid, f)
	}
	var status *string
	if f.Status != "" {
		v := string(f.Status)
		status = &v
	}
	rows, err := sqlcgen.New(s.db).ListDiscrepancies(ctx, sqlcgen.ListDiscrepanciesParams{
		Status: status, RowLimit: int32(f.Limit), // bounded by MaxListLimit above
	})
	if err != nil {
		return nil, fmt.Errorf("discrepancy: list: %w", err)
	}
	out := make([]Discrepancy, len(rows))
	for i, r := range rows {
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
		out[i] = d
	}
	return out, nil
}
