package httpserver

import (
	"context"
	"net/http"
	"time"
)

// CheckStatus is the result of one readiness check.
type CheckStatus string

// Readiness check results reported by /readyz.
const (
	CheckOK      CheckStatus = "ok"
	CheckFail    CheckStatus = "fail"
	CheckPending CheckStatus = "pending"
)

const (
	statusOK          = "ok"
	statusUnavailable = "unavailable"
)

// ReadinessCheck is one named dependency check of /readyz.
type ReadinessCheck struct {
	Name  string
	Check func(ctx context.Context) CheckStatus
}

type healthBody struct {
	Status string                 `json:"status"`
	Checks map[string]CheckStatus `json:"checks,omitempty"`
}

// Healthz reports that the process is up. It checks no dependency.
func Healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, healthBody{Status: statusOK})
	}
}

// Readyz runs every check with its own timeout and answers 200 only when all are ok.
func Readyz(timeout time.Duration, checks ...ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := healthBody{Status: statusOK, Checks: make(map[string]CheckStatus, len(checks))}
		for _, c := range checks {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			result := c.Check(ctx)
			cancel()
			body.Checks[c.Name] = result
			if result != CheckOK {
				body.Status = statusUnavailable
			}
		}
		status := http.StatusOK
		if body.Status != statusOK {
			status = http.StatusServiceUnavailable
		}
		WriteJSON(w, status, body)
	}
}
