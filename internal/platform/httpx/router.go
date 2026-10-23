package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// APIPrefix is the mount point for versioned business-module routes.
const APIPrefix = "/api/v1"

// LivenessPath is the liveness probe endpoint.
const LivenessPath = "/health/live"

// Module is implemented by business modules that expose HTTP routes. The
// router passed to RegisterRoutes is already scoped to APIPrefix.
type Module interface {
	RegisterRoutes(r chi.Router)
}

// ModuleFunc adapts a function to the Module interface.
type ModuleFunc func(r chi.Router)

func (f ModuleFunc) RegisterRoutes(r chi.Router) { f(r) }

// RouterDeps are the platform dependencies of the public API router.
type RouterDeps struct {
	Logger   *slog.Logger
	Observer RequestObserver
}

// NewRouter builds the public API router.
//
// Middleware order, outermost first:
//  1. RequestID  - establishes the ID used by every later log record.
//  2. AccessLog  - sees the final status, including recovered panics.
//  3. Metrics    - counts normal requests and recovered failures alike.
//  4. Recoverer  - innermost, so the 500 it writes is visible to 2 and 3.
func NewRouter(deps RouterDeps, modules ...Module) http.Handler {
	r := chi.NewRouter()
	r.Use(
		RequestID,
		AccessLog(deps.Logger, isProbe),
		Metrics(deps.Observer),
		Recoverer(deps.Logger),
	)
	r.NotFound(NotFound)
	r.MethodNotAllowed(methodNotAllowedHandler)

	r.Get(LivenessPath, liveness)

	r.Route(APIPrefix, func(api chi.Router) {
		for _, m := range modules {
			m.RegisterRoutes(api)
		}
	})

	return r
}

func isProbe(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/health/")
}

func liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

var allowCandidates = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
	http.MethodConnect, http.MethodTrace,
}

// methodNotAllowedHandler replaces chi's default 405 handler, which is the only
// place chi sets the Allow header, so the allowed methods are recomputed here.
func methodNotAllowedHandler(w http.ResponseWriter, r *http.Request) {
	var allowed []string
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.Routes != nil {
		for _, m := range allowCandidates {
			if rctx.Routes.Match(chi.NewRouteContext(), m, r.URL.Path) {
				allowed = append(allowed, m)
			}
		}
	}
	methodNotAllowed(w, allowed)
}
