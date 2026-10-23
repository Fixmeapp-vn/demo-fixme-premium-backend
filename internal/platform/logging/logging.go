// Package logging builds the structured JSON logger shared by all binaries.
package logging

import (
	"context"
	"io"
	"log/slog"
)

// Options describe the static attributes attached to every log record.
type Options struct {
	Service string
	Env     string
	Level   slog.Leveler
}

// New returns a JSON logger writing to w. Records logged with a context
// carrying a request ID (see WithRequestID) include a "request_id" attribute.
func New(w io.Writer, opts Options) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: opts.Level})
	return slog.New(contextHandler{Handler: h}).With(
		slog.String("service", opts.Service),
		slog.String("env", opts.Env),
	)
}

// Discard returns a logger that drops every record, for tests.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type requestIDKey struct{}

// WithRequestID returns a copy of ctx carrying the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the request ID stored in ctx, if any.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey{}).(string)
	return id, ok && id != ""
}

type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := RequestIDFromContext(ctx); ok {
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
