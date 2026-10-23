package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// UnmatchedRoute is the route label used for requests that matched no route
// (404s and 405s), so raw request paths never reach logs or metric labels.
const UnmatchedRoute = "unmatched"

// RouteTemplate returns the matched chi route pattern (e.g. "/api/v1/users/{id}"),
// or UnmatchedRoute. It is only complete after the router has run.
func RouteTemplate(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return UnmatchedRoute
	}
	if p := rctx.RoutePattern(); p != "" && !strings.HasSuffix(p, "/*") {
		return p
	}
	return UnmatchedRoute
}

// RequestObserver records per-request metrics.
type RequestObserver interface {
	ObserveHTTPRequest(method, route string, status int, duration time.Duration)
}

// AccessLog logs one structured record per request after it completes.
// Requests for which quiet returns true are logged at debug level.
func AccessLog(logger *slog.Logger, quiet func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			level := slog.LevelInfo
			if quiet != nil && quiet(r) {
				level = slog.LevelDebug
			}
			logger.LogAttrs(r.Context(), level, "http request",
				slog.String("method", r.Method),
				slog.String("route", RouteTemplate(r)),
				slog.Int("status", statusOf(ww)),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			)
		})
	}
}

// Metrics reports every completed request to obs.
func Metrics(obs RequestObserver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			obs.ObserveHTTPRequest(r.Method, RouteTemplate(r), statusOf(ww), time.Since(start))
		})
	}
}

// Recoverer converts handler panics into 500 Problem responses and logs the
// panic value with its stack trace. If the response was already committed it
// cannot be replaced, so only the log record is produced.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				committed := ww.Status() != 0
				logger.ErrorContext(r.Context(), "panic recovered",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
					slog.String("method", r.Method),
					slog.String("route", RouteTemplate(r)),
					slog.Bool("response_committed", committed),
				)
				if !committed {
					InternalError(ww, r)
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

func statusOf(ww middleware.WrapResponseWriter) int {
	if s := ww.Status(); s != 0 {
		return s
	}
	return http.StatusOK
}
