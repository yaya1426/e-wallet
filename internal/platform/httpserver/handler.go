package httpserver

import (
	"log/slog"
	"net/http"
)

// NewHandler wraps mux with the standard middleware chain: request ID → access log and
// metrics → panic recovery → JSON 404/405 for requests mux cannot route.
// observer may be nil.
func NewHandler(mux *http.ServeMux, log *slog.Logger, observer Observer) http.Handler {
	h := jsonFallback(mux)
	h = recoverPanic(log, h)
	h = accessLog(log, observer, h)
	return requestID(h)
}

// jsonFallback replaces ServeMux's plain-text 404 and 405 responses with the TDR-013
// error body. Routed requests go to mux unchanged so it can set r.Pattern.
func jsonFallback(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		// No pattern matched: ask ServeMux's own fallback handler which status it would
		// send (404, or 405 with an Allow header) without writing its plain-text body.
		probe := &headerProbe{header: http.Header{}}
		handler.ServeHTTP(probe, r)
		switch probe.status {
		case http.StatusMethodNotAllowed:
			if allow := probe.header.Get("Allow"); allow != "" {
				w.Header().Set("Allow", allow)
			}
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method not allowed.")
		case http.StatusNotFound:
			WriteError(w, http.StatusNotFound, CodeNotFound, "Resource not found.")
		default:
			// Redirects (e.g. path cleaning) are passed through as ServeMux sends them.
			mux.ServeHTTP(w, r)
		}
	})
}

// headerProbe captures the status and headers a handler writes and discards the body.
type headerProbe struct {
	header http.Header
	status int
}

func (p *headerProbe) Header() http.Header { return p.header }

func (p *headerProbe) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return len(b), nil
}

func (p *headerProbe) WriteHeader(status int) {
	if p.status == 0 {
		p.status = status
	}
}
