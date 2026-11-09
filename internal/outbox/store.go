package outbox

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/yaya1426/e-wallet/internal/outbox/internal/sqlcgen"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/queue"
)

func queries(q Querier) *sqlcgen.Queries { return sqlcgen.New(q) }

// store implements queue.Store over outbox_events. Times come from the clock, not now() in
// SQL, so tests can move time (e.g. to expire a lease).
type store struct {
	db    Querier
	clock clock.Clock
	obs   Observer
}

var _ queue.Store = store{}

func (s store) Claim(ctx context.Context, owner string, n int, lease time.Duration) ([]queue.Job, error) {
	now := s.clock.Now()
	rows, err := queries(s.db).ClaimEvents(ctx, sqlcgen.ClaimEventsParams{
		Owner: owner, LockedUntil: now.Add(lease), Now: now, BatchSize: toInt32(n),
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: claim: %w", err)
	}
	jobs := make([]queue.Job, len(rows))
	for i, r := range rows {
		jobs[i] = queue.Job{ID: r.ID, Type: r.EventType, Payload: r.Payload, Attempts: int(r.Attempts)}
	}
	return jobs, nil
}

func (s store) MarkDone(ctx context.Context, job queue.Job) error {
	_, err := queries(s.db).MarkDone(ctx, sqlcgen.MarkDoneParams{Now: s.clock.Now(), ID: job.ID, Attempts: toInt32(job.Attempts)})
	return wrap("mark done", err)
}

func (s store) MarkRetry(ctx context.Context, job queue.Job, next time.Time, lastErr string) error {
	_, err := queries(s.db).MarkRetry(ctx, sqlcgen.MarkRetryParams{
		NextAttemptAt: next, LastError: lastErr, ID: job.ID, Attempts: toInt32(job.Attempts),
	})
	return wrap("mark retry", err)
}

func (s store) MarkDead(ctx context.Context, job queue.Job, lastErr string) error {
	n, err := queries(s.db).MarkDead(ctx, sqlcgen.MarkDeadParams{LastError: lastErr, ID: job.ID, Attempts: toInt32(job.Attempts)})
	if err != nil {
		return wrap("mark dead", err)
	}
	if n == 1 { // a stale mark changed nothing and is not counted
		s.obs.ObserveOutboxDead()
	}
	return nil
}

// MarkFailed finishes the event as DONE with the reason in last_error: for the outbox a
// business rejection is a completed delivery (TDR-006 §3).
func (s store) MarkFailed(ctx context.Context, job queue.Job, reason string) error {
	_, err := queries(s.db).MarkDone(ctx, sqlcgen.MarkDoneParams{
		Now: s.clock.Now(), LastError: &reason, ID: job.ID, Attempts: toInt32(job.Attempts),
	})
	return wrap("mark failed", err)
}

func wrap(op string, err error) error {
	if err != nil {
		return fmt.Errorf("outbox: %s: %w", op, err)
	}
	return nil
}

// toInt32 converts batch sizes and attempt counts (far below the int32 range), clamping.
func toInt32(n int) int32 {
	return int32(max(math.MinInt32, min(n, math.MaxInt32)))
}
