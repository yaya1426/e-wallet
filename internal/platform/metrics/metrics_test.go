package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetrics_Exposed(t *testing.T) {
	m := New()
	m.ObserveHTTP(http.MethodGet, "GET /healthz", http.StatusOK, 3*time.Millisecond)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, body, "go_goroutines")
	assert.Contains(t, body, "process_")
	assert.Contains(t, body, `http_requests_total{method="GET",route="GET /healthz",status="200"} 1`)
	assert.Contains(t, body, `http_request_duration_seconds_count{method="GET",route="GET /healthz"} 1`)
}
