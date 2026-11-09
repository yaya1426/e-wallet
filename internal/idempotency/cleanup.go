package idempotency

import (
	"context"
	"log/slog"
	"time"
)

// cleanupBatchSize bounds each DELETE so the cleanup never holds many row locks at once.
const cleanupBatchSize = 1000

// Cleanup deletes expired keys every interval (IDEMPOTENCY_CLEANUP_INTERVAL, TDR-005: hourly).
type Cleanup struct {
	svc      *Service
	db       Querier
	interval time.Duration
	log      *slog.Logger
}

// NewCleanup returns the cleanup job. db is the pool; a nil log discards the job's logs.
func NewCleanup(svc *Service, db Querier, interval time.Duration, log *slog.Logger) *Cleanup {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Cleanup{svc: svc, db: db, interval: interval, log: log}
}

// Run deletes expired keys on every tick until ctx ends. A failed run is logged and tried
// again on the next tick. It returns nil when ctx ends.
func (c *Cleanup) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.RunOnce(ctx)
		}
	}
}

// RunOnce deletes all expired keys now.
func (c *Cleanup) RunOnce(ctx context.Context) {
	deleted, err := c.svc.DeleteExpired(ctx, c.db, cleanupBatchSize)
	if err != nil {
		c.log.WarnContext(ctx, "idempotency cleanup failed", slog.Any("error", err))
		return
	}
	if deleted > 0 {
		c.log.InfoContext(ctx, "idempotency keys cleaned up", slog.Int64("deleted", deleted))
	}
}
