package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// T024 AC-1.
func TestClientIP(t *testing.T) {
	tests := []struct {
		name, header, value, remote, want string
	}{
		{"no header configured: connection host", "", "198.51.100.1", "203.0.113.7:51234", "203.0.113.7"},
		{"X-Forwarded-For ignored when not configured", "", "", "203.0.113.7:51234", "203.0.113.7"},
		{"configured header", "do-connecting-ip", "198.51.100.1", "10.0.0.2:443", "198.51.100.1"},
		{"first of a list", "X-Forwarded-For", "198.51.100.1, 10.0.0.1", "10.0.0.2:443", "198.51.100.1"},
		{"IPv6", "do-connecting-ip", "2001:db8::1", "10.0.0.2:443", "2001:db8::1"},
		{"invalid value falls back", "do-connecting-ip", "unknown", "10.0.0.2:443", "10.0.0.2"},
		{"absent header falls back", "do-connecting-ip", "", "10.0.0.2:443", "10.0.0.2"},
		{"IPv6 connection", "", "", "[2001:db8::2]:443", "2001:db8::2"},
		{"remote without port", "", "", "10.0.0.3", "10.0.0.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := clientIPMiddleware(tt.header, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ClientIP(r) }))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			if tt.value != "" {
				name := tt.header
				if name == "" {
					name = "X-Forwarded-For"
				}
				req.Header.Set(name, tt.value)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			assert.Equal(t, tt.want, got)
		})
	}
	// Without the middleware: the connection host.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:1"
	assert.Equal(t, "203.0.113.9", ClientIP(req))
}

// T024 AC-4.
func TestAccessLog_ClientIP(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) {})
	h := NewHandler(mux, log, nil, WithClientIPHeader("do-connecting-ip"))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("do-connecting-ip", "198.51.100.42")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Contains(t, buf.String(), `"client_ip":"198.51.100.42"`)
}
