package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yaya1426/e-wallet/internal/discrepancy"
	"github.com/yaya1426/e-wallet/internal/outbox"
	"github.com/yaya1426/e-wallet/internal/platform/clock"
	"github.com/yaya1426/e-wallet/internal/platform/httpserver"
	"github.com/yaya1426/e-wallet/internal/reconciliation"
	"github.com/yaya1426/e-wallet/internal/transaction"
	"github.com/yaya1426/e-wallet/internal/webhook"
)

// Overall statuses of the ops health summary (T028).
const (
	opsOK       = "ok"
	opsWarning  = "warning"
	opsCritical = "critical"
)

// staleRunFactor: a job whose last run is older than this many intervals is late.
const staleRunFactor = 3

// opsHealth serves GET /internal/v1/health/ops: what needs an operator, in one call. It is
// assembled here, in the composition root, from each module's own counts.
type opsHealth struct {
	pool          *pgxpool.Pool
	discrepancies *discrepancy.Service
	outbox        *outbox.Service
	inbox         *webhook.Service
	transactions  *transaction.Service
	recon         *reconciliation.Scheduler
	reconEnabled  bool
	stuckAge      time.Duration
	clock         clock.Clock
	started       time.Time
	log           *slog.Logger
}

type opsCounts struct {
	Pending int64 `json:"pending"`
	Dead    int64 `json:"dead"`
}

type opsJob struct {
	Name           string     `json:"name"`
	LastStatus     *string    `json:"last_status"`
	LastFinishedAt *time.Time `json:"last_finished_at"`
	Late           bool       `json:"late"`
}

type opsBody struct {
	Status        string           `json:"status"`
	Discrepancies map[string]int64 `json:"discrepancies"`
	Stuck         map[string]int64 `json:"stuck"`
	Outbox        opsCounts        `json:"outbox"`
	Inbox         opsCounts        `json:"inbox"`
	Jobs          []opsJob         `json:"jobs"`
}

func (o *opsHealth) serve(w http.ResponseWriter, r *http.Request) {
	body, err := o.summary(r.Context())
	if err != nil {
		o.log.ErrorContext(r.Context(), "ops health failed", slog.Any("error", err))
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternal, "Internal server error.")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, body)
}

// summary collects the counts and derives the status:
//   - critical: an open CRITICAL discrepancy, or a DEAD outbox/inbox event;
//   - warning: an open WARNING discrepancy, a stuck transaction, or a job whose last run
//     FAILED or is late (none within 3 intervals while reconciliation runs here);
//   - ok otherwise.
func (o *opsHealth) summary(ctx context.Context) (opsBody, error) {
	open, err := o.discrepancies.CountOpen(ctx)
	if err != nil {
		return opsBody{}, err
	}
	now := o.clock.Now()
	stuck, err := o.transactions.CountStuck(ctx, o.pool, now.Add(-o.stuckAge))
	if err != nil {
		return opsBody{}, err
	}
	out, err := o.outbox.Counts(ctx)
	if err != nil {
		return opsBody{}, err
	}
	in, err := o.inbox.Counts(ctx)
	if err != nil {
		return opsBody{}, err
	}
	runs, err := o.recon.LastRuns(ctx)
	if err != nil {
		return opsBody{}, err
	}
	body := opsBody{
		Discrepancies: map[string]int64{"open_critical": open.Critical, "open_warning": open.Warning},
		Stuck:         map[string]int64{},
		Outbox:        opsCounts{Pending: out.Pending, Dead: out.Dead},
		Inbox:         opsCounts{Pending: in.Pending, Dead: in.Dead},
		Jobs:          []opsJob{},
	}
	var stuckTotal int64
	for typ, n := range stuck {
		body.Stuck[string(typ)] = n
		stuckTotal += n
	}
	jobTrouble := false
	for _, j := range o.recon.Registered() {
		job := opsJob{Name: j.Name}
		limit := staleRunFactor * j.Interval
		if run, ok := runs[j.Name]; ok {
			status, finished := run.Status, run.FinishedAt.UTC()
			job.LastStatus, job.LastFinishedAt = &status, &finished
			job.Late = o.reconEnabled && now.Sub(run.FinishedAt) > limit
			jobTrouble = jobTrouble || status == reconciliation.StatusFailed
		} else {
			// Never ran: late only once this instance has been up long enough to have run it.
			job.Late = o.reconEnabled && now.Sub(o.started) > limit
		}
		jobTrouble = jobTrouble || job.Late
		body.Jobs = append(body.Jobs, job)
	}
	switch {
	case open.Critical > 0 || out.Dead > 0 || in.Dead > 0:
		body.Status = opsCritical
	case open.Warning > 0 || stuckTotal > 0 || jobTrouble:
		body.Status = opsWarning
	default:
		body.Status = opsOK
	}
	return body, nil
}
