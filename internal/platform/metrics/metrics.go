// Package metrics exposes Prometheus metrics for the process.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a Prometheus registry with the Go/process collectors and HTTP metrics.
type Metrics struct {
	registry        *prometheus.Registry
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	txRetries       *prometheus.CounterVec
	txTryAgain      prometheus.Counter
	queueJobs       *prometheus.CounterVec
	queueClaimed    *prometheus.CounterVec
	queueDuration   *prometheus.HistogramVec
	outboxPending   prometheus.Gauge
	outboxOldestAge prometheus.Gauge
	outboxDead      prometheus.Counter
	bankCalls       *prometheus.HistogramVec
	webhooks        *prometheus.CounterVec
	reconRuns       *prometheus.CounterVec
	reconDuration   *prometheus.HistogramVec
	reconItems      *prometheus.CounterVec
	reconStuck      *prometheus.GaugeVec
}

// New creates the registry and registers all collectors.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests served, by method, route pattern and status code.",
		}, []string{"method", "route", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration, by method and route pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		txRetries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "db_tx_retries_total",
			Help: "Database transactions retried after a conflict, by reason (serialization_failure, deadlock, lock_timeout).",
		}, []string{"reason"}),
		txTryAgain: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_tx_try_again_total",
			Help: "Database transactions abandoned with TRY_AGAIN after all retries.",
		}),
	}
	m.queueJobs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "queue_jobs_total",
		Help: "Queue jobs handled, by queue and result (done, retry, dead, failed).",
	}, []string{"queue", "result"})
	m.queueClaimed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "queue_claimed_total",
		Help: "Queue jobs claimed by the dispatcher, by queue.",
	}, []string{"queue"})
	m.queueDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "queue_handler_duration_seconds",
		Help:    "Time spent in queue handlers, by queue.",
		Buckets: prometheus.DefBuckets,
	}, []string{"queue"})
	m.outboxPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_pending_total",
		Help: "Outbox events not finished yet (PENDING or PROCESSING).",
	})
	m.outboxOldestAge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_oldest_pending_age_seconds",
		Help: "Age of the oldest unfinished outbox event (0 when there is none).",
	})
	m.outboxDead = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "outbox_dead_total",
		Help: "Outbox events that became DEAD after the last attempt.",
	})
	m.bankCalls = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "bank_call_duration_seconds",
		Help:    "Bank gateway call duration, by operation and result (ok, rejected, temporary, not_found).",
		Buckets: prometheus.DefBuckets,
	}, []string{"op", "result"})
	m.webhooks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "webhook_received_total",
		Help: "Bank webhooks received, by result (stored, duplicate, invalid_signature, malformed, error).",
	}, []string{"result"})
	m.reconRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "recon_job_runs_total",
		Help: "Reconciliation job ticks, by job and status (success, failed, skipped: another instance held the lock).",
	}, []string{"job", "status"})
	m.reconDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "recon_job_duration_seconds",
		Help:    "Duration of reconciliation job runs, by job.",
		Buckets: prometheus.DefBuckets,
	}, []string{"job"})
	m.reconItems = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "recon_items_total",
		Help: "Items handled by reconciliation jobs, by job and kind (checked, fixed).",
	}, []string{"job", "kind"})
	m.reconStuck = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "recon_stuck_transactions",
		Help: "Top-ups and withdrawals not final after RECON_STUCK_AGE, by type (set by stuck_alert; alert on > 0).",
	}, []string{"type"})
	m.registry.MustRegister(
		m.reconRuns, m.reconDuration, m.reconItems, m.reconStuck,
		m.bankCalls,
		m.queueJobs, m.queueClaimed, m.queueDuration,
		m.outboxPending, m.outboxOldestAge, m.outboxDead, m.webhooks,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests,
		m.requestDuration,
		m.txRetries,
		m.txTryAgain,
	)
	// Pre-create the retry reasons so the series exist (at 0) before the first retry.
	for _, reason := range []string{"serialization_failure", "deadlock", "lock_timeout"} {
		m.txRetries.WithLabelValues(reason)
	}
	return m
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// ObserveHTTP records one finished HTTP request.
func (m *Metrics) ObserveHTTP(method, route string, status int, duration time.Duration) {
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.requestDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

// ObserveTxRetry counts one retried database transaction (database.TxObserver).
func (m *Metrics) ObserveTxRetry(reason string) {
	m.txRetries.WithLabelValues(reason).Inc()
}

// ObserveTxTryAgain counts one transaction abandoned after all retries (database.TxObserver).
func (m *Metrics) ObserveTxTryAgain() {
	m.txTryAgain.Inc()
}

// ObserveQueueClaimed counts jobs claimed by a queue dispatcher (queue.Observer).
func (m *Metrics) ObserveQueueClaimed(queue string, n int) {
	m.queueClaimed.WithLabelValues(queue).Add(float64(n))
}

// ObserveQueueJob counts one handled job and its handler duration (queue.Observer).
func (m *Metrics) ObserveQueueJob(queue, result string, handlerDuration time.Duration) {
	m.queueJobs.WithLabelValues(queue, result).Inc()
	m.queueDuration.WithLabelValues(queue).Observe(handlerDuration.Seconds())
}

// ObserveOutboxBacklog sets the outbox backlog gauges (outbox.Observer).
func (m *Metrics) ObserveOutboxBacklog(pending int64, oldestAge time.Duration) {
	m.outboxPending.Set(float64(pending))
	m.outboxOldestAge.Set(oldestAge.Seconds())
}

// ObserveOutboxDead counts one DEAD outbox event (outbox.Observer).
func (m *Metrics) ObserveOutboxDead() {
	m.outboxDead.Inc()
}

// ObserveWebhookReceived counts one bank webhook by result (webhook.Observer).
func (m *Metrics) ObserveWebhookReceived(result string) {
	m.webhooks.WithLabelValues(result).Inc()
}

// ObserveReconRun counts one reconciliation tick; skipped ticks have no duration
// (reconciliation.Observer).
func (m *Metrics) ObserveReconRun(job, status string, duration time.Duration) {
	m.reconRuns.WithLabelValues(job, status).Inc()
	if status != "skipped" {
		m.reconDuration.WithLabelValues(job).Observe(duration.Seconds())
	}
}

// ObserveReconItems counts the items a reconciliation run checked and fixed
// (reconciliation.Observer).
func (m *Metrics) ObserveReconItems(job string, checked, fixed int) {
	m.reconItems.WithLabelValues(job, "checked").Add(float64(checked))
	m.reconItems.WithLabelValues(job, "fixed").Add(float64(fixed))
}

// ObserveStuckTransactions sets the stuck-transaction gauge of a type (reconciliation.StuckObserver).
func (m *Metrics) ObserveStuckTransactions(txnType string, n int64) {
	m.reconStuck.WithLabelValues(txnType).Set(float64(n))
}

// ObserveBankCall records one bank gateway call (banksim.Observer).
func (m *Metrics) ObserveBankCall(op, result string, duration time.Duration) {
	m.bankCalls.WithLabelValues(op, result).Observe(duration.Seconds())
}
