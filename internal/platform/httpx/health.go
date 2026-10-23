package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// ReadinessPath is the readiness probe endpoint. Unlike LivenessPath, it
// reflects the health of external dependencies such as the database, so a
// failing dependency makes it return 503 while liveness stays 200.
const ReadinessPath = "/health/ready"

// DefaultReadinessTimeout bounds how long all readiness checks together may
// take before the probe reports the service unavailable.
const DefaultReadinessTimeout = 2 * time.Second

// HealthCheck reports whether a single external dependency is reachable.
// Name identifies the dependency in readiness responses and must not reveal
// secrets such as connection strings; Check must honour ctx cancellation.
type HealthCheck interface {
	Name() string
	Check(ctx context.Context) error
}

type readinessBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// readiness returns a handler that reports the service ready only when every
// check passes within timeout. With no checks registered it always reports
// ready. Check errors are never written to the response so that connection
// strings and other internals cannot leak to clients.
func readiness(checks []HealthCheck, timeout time.Duration) http.HandlerFunc {
	if timeout <= 0 {
		timeout = DefaultReadinessTimeout
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		results := make(map[string]string, len(checks))
		ready := true
		for _, c := range checks {
			if err := c.Check(ctx); err != nil {
				ready = false
				results[c.Name()] = "unavailable"
				continue
			}
			results[c.Name()] = "ok"
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(readinessBody{Status: "unavailable", Checks: results})
			return
		}
		_ = json.NewEncoder(w).Encode(readinessBody{Status: "ok", Checks: results})
	}
}
