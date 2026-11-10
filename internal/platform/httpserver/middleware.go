package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/yaya1426/e-wallet/internal/platform/logger"
)

// HeaderRequestID carries the request ID on requests and responses (TDR-013).
const HeaderRequestID = "X-Request-ID"

// maxRequestIDLength bounds incoming request IDs so they cannot flood logs.
const maxRequestIDLength = 128

// routeUnmatched is the route label for requests that matched no pattern.
const routeUnmatched = "unmatched"

// methodOther replaces non-standard request methods in observations.
const methodOther = "OTHER"

// standardMethods bounds the method values passed to the Observer: net/http accepts any
// token as a method, so raw client input must never become a metric label.
var standardMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodConnect: true,
	http.MethodOptions: true, http.MethodTrace: true,
}

// Observer records one finished HTTP request, e.g. as metrics. method is always a
// standard HTTP method or "OTHER"; route is a registered pattern or "unmatched".
type Observer interface {
	ObserveHTTP(method, route string, status int, duration time.Duration)
}

// quietPaths are logged at debug level so probes and scrapes do not flood the logs.
var quietPaths = map[string]bool{"/healthz": true, "/readyz": true, "/metrics": true}

// requestID keeps a valid incoming X-Request-ID or generates a UUIDv7, sets it on the
// response and stores it in the request context for logging.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(logger.WithRequestID(r.Context(), id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for _, c := range id {
		if c < '!' || c > '~' { // printable ASCII without spaces
			return false
		}
	}
	return true
}

func newRequestID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// accessLog logs one line per request and reports it to the observer (if any).
func accessLog(log *slog.Logger, observer Observer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		duration := time.Since(start)

		route := r.Pattern
		if route == "" {
			route = routeUnmatched
		}
		if observer != nil {
			observer.ObserveHTTP(methodLabel(r.Method), route, rec.status, duration)
		}

		level := slog.LevelInfo
		if quietPaths[r.URL.Path] {
			level = slog.LevelDebug
		}
		log.LogAttrs(r.Context(), level, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("route", route),
			slog.String("client_ip", ClientIP(r)),
			slog.Int("status", rec.status),
			slog.Duration("duration", duration),
		)
	})
}

// methodLabel returns method if it is a standard HTTP method, else "OTHER".
func methodLabel(method string) string {
	if standardMethods[method] {
		return method
	}
	return methodOther
}

// recoverPanic turns a handler panic into a 500 with the error body and logs the stack.
func recoverPanic(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v) // net/http uses this to abort a response on purpose
			}
			log.ErrorContext(r.Context(), "panic in http handler",
				slog.Any("panic", v),
				slog.String("stack", string(debug.Stack())),
			)
			WriteError(w, http.StatusInternalServerError, CodeInternal, "Internal server error.")
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder remembers the status code written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
