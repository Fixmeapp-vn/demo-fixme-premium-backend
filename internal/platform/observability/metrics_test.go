package observability_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/observability"
)

func TestObserveHTTPRequestCountsByLabels(t *testing.T) {
	m := observability.NewMetrics()

	m.ObserveHTTPRequest("GET", "/api/v1/users/{id}", 200, 10*time.Millisecond)
	m.ObserveHTTPRequest("GET", "/api/v1/users/{id}", 200, 20*time.Millisecond)
	m.ObserveHTTPRequest("GET", "/api/v1/users/{id}", 404, time.Millisecond)
	m.ObserveHTTPRequest("BREW", "unmatched", 405, time.Millisecond)

	want := `
# HELP http_requests_total Total number of HTTP requests handled, by method, route template and status.
# TYPE http_requests_total counter
http_requests_total{method="GET",route="/api/v1/users/{id}",status="200"} 2
http_requests_total{method="GET",route="/api/v1/users/{id}",status="404"} 1
http_requests_total{method="OTHER",route="unmatched",status="405"} 1
`
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(want), "http_requests_total"); err != nil {
		t.Error(err)
	}
	if n := testutil.CollectAndCount(m.Registry(), "http_request_duration_seconds"); n != 3 {
		t.Errorf("duration series = %d, want 3", n)
	}
}

func TestHandlerExposesHTTPAndRuntimeMetrics(t *testing.T) {
	m := observability.NewMetrics()
	m.ObserveHTTPRequest("GET", "/health/live", 200, time.Millisecond)

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	for _, name := range []string{"http_requests_total", "http_request_duration_seconds_bucket", "go_goroutines", "process_cpu_seconds_total"} {
		if !strings.Contains(string(body), name) {
			t.Errorf("metrics output missing %s", name)
		}
	}
}

func TestHandlerServesOnlyMetrics(t *testing.T) {
	rec := httptest.NewRecorder()
	observability.NewMetrics().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
