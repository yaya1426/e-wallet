package httpserver

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// Option configures NewHandler.
type Option func(*options)

type options struct {
	clientIPHeader string
}

// WithClientIPHeader makes the client IP come from the given request header (TDR-018 §1),
// e.g. "do-connecting-ip" behind DigitalOcean's proxy. "" keeps the connection address.
func WithClientIPHeader(header string) Option {
	return func(o *options) { o.clientIPHeader = header }
}

type clientIPKey struct{}

// clientIPMiddleware resolves the client IP once per request and stores it in the context.
func clientIPMiddleware(header string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := resolveClientIP(r, header)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip)))
	})
}

// ClientIP returns the client IP of the request: the one the handler chain resolved
// (WithClientIPHeader), else the host part of the connection address.
func ClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return ip
	}
	return remoteHost(r)
}

// resolveClientIP takes the first address of header when it is set, present and a valid IP;
// otherwise the connection's host.
func resolveClientIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			first, _, _ := strings.Cut(v, ",")
			if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
				return ip.String()
			}
		}
	}
	return remoteHost(r)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
