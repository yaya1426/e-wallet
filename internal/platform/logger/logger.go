// Package logger builds the structured JSON logger used by every process.
package logger

import (
	"context"
	"io"
	"log/slog"
)

type requestIDKey struct{}

// New returns a JSON logger writing to w at the given level. Records logged with a
// context that carries a request ID get a "request_id" attribute.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(contextHandler{Handler: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})})
}

// WithRequestID returns a copy of ctx carrying the request ID.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestID returns the request ID stored in ctx, or "" if there is none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// contextHandler adds values carried by the context to every record.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}
