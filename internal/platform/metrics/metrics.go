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
	m.registry.MustRegister(
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
