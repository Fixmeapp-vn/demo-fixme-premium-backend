package httpx_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/httpx"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
)

type observation struct {
	method, route string
	status        int
}

type fakeObserver struct {
	mu   sync.Mutex
	seen []observation
}

func (f *fakeObserver) ObserveHTTPRequest(method, route string, status int, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, observation{method, route, status})
}

func (f *fakeObserver) last(t *testing.T) observation {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		t.Fatal("no request observed")
	}
	return f.seen[len(f.seen)-1]
}

type harness struct {
	handler http.Handler
	logs    *bytes.Buffer
	obs     *fakeObserver
}

func newHarness(t *testing.T, modules ...httpx.Module) *harness {
	t.Helper()
	logs := &bytes.Buffer{}
	obs := &fakeObserver{}
	logger := logging.New(logs, logging.Options{Service: "api", Env: "test", Level: slog.LevelDebug})
	return &harness{
		handler: httpx.NewRouter(httpx.RouterDeps{Logger: logger, Observer: obs}, modules...),
		logs:    logs,
		obs:     obs,
	}
}

func newReadinessHarness(t *testing.T, timeout time.Duration, checks ...httpx.HealthCheck) *harness {
	t.Helper()
	logs := &bytes.Buffer{}
	obs := &fakeObserver{}
	logger := logging.New(logs, logging.Options{Service: "api", Env: "test", Level: slog.LevelDebug})
	return &harness{
		handler: httpx.NewRouter(httpx.RouterDeps{
			Logger:           logger,
			Observer:         obs,
			ReadinessChecks:  checks,
			ReadinessTimeout: timeout,
		}),
		logs: logs,
		obs:  obs,
	}
}

type fakeCheck struct {
	name   string
	err    error
	called bool
}

func (f *fakeCheck) Name() string { return f.name }

func (f *fakeCheck) Check(context.Context) error {
	f.called = true
	return f.err
}

func (h *harness) do(method, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *harness) records(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(h.logs.Bytes()))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON log line %q: %v", sc.Text(), err)
		}
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

var usersModule = httpx.ModuleFunc(func(r chi.Router) {
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	r.Post("/users", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	r.Get("/boom", func(http.ResponseWriter, *http.Request) {
		panic("secret internal detail")
	})
	r.Get("/boom-after-write", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("late failure")
	})
})

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != httpx.ProblemContentType {
		t.Fatalf("Content-Type = %q, want %q", ct, httpx.ProblemContentType)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body %q is not a problem: %v", rec.Body.String(), err)
	}
	return p
}

func TestLiveness(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/health/live", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %s", body)
	}
}

func TestReadinessWithoutChecksIsOK(t *testing.T) {
	h := newReadinessHarness(t, 0)
	rec := h.do(http.MethodGet, "/health/ready", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("body = %s", body)
	}
}

func TestReadinessPassingCheck(t *testing.T) {
	check := &fakeCheck{name: "user_db"}
	h := newReadinessHarness(t, 0, check)
	rec := h.do(http.MethodGet, "/health/ready", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !check.called {
		t.Error("readiness did not invoke the registered check")
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok","checks":{"user_db":"ok"}}` {
		t.Errorf("body = %s", body)
	}
}

func TestReadinessFailingCheck(t *testing.T) {
	check := &fakeCheck{name: "user_db", err: errors.New("dial tcp 10.0.0.1:5432: connection refused")}
	h := newReadinessHarness(t, 0, check)
	rec := h.do(http.MethodGet, "/health/ready", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"unavailable","checks":{"user_db":"unavailable"}}` {
		t.Errorf("body = %s", body)
	}
	// The underlying error must never reach the client.
	if strings.Contains(rec.Body.String(), "connection refused") || strings.Contains(rec.Body.String(), "10.0.0.1") {
		t.Errorf("readiness body leaks check error: %s", rec.Body.String())
	}
}

func TestLivenessNeverInvokesChecks(t *testing.T) {
	check := &fakeCheck{name: "user_db", err: errors.New("down")}
	h := newReadinessHarness(t, 0, check)

	rec := h.do(http.MethodGet, "/health/live", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want 200", rec.Code)
	}
	if check.called {
		t.Error("liveness invoked a readiness check; it must stay dependency-free")
	}
}

func TestNotFoundProblem(t *testing.T) {
	h := newHarness(t, usersModule)

	for _, path := range []string{"/nope", "/api/v1/nope", "/api/v1/users/1/extra"} {
		t.Run(path, func(t *testing.T) {
			rec := h.do(http.MethodGet, path, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			want := httpx.Problem{
				Type:   "about:blank",
				Title:  "Not Found",
				Status: 404,
				Detail: "The requested resource was not found.",
				Code:   httpx.CodeNotFound,
			}
			if got := decodeProblem(t, rec); got != want {
				t.Errorf("problem = %+v, want %+v", got, want)
			}
		})
	}
}

func TestMethodNotAllowedProblem(t *testing.T) {
	h := newHarness(t, usersModule)

	tests := []struct {
		method, path string
		allow        []string
	}{
		{http.MethodPost, "/health/live", []string{"GET"}},
		{http.MethodDelete, "/api/v1/users", []string{"POST"}},
		{http.MethodPut, "/api/v1/users/42", []string{"GET"}},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := h.do(tt.method, tt.path, nil)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			p := decodeProblem(t, rec)
			if p.Status != 405 || p.Code != httpx.CodeMethodNotAllowed || p.Title != "Method Not Allowed" {
				t.Errorf("problem = %+v", p)
			}
			if got := rec.Header().Values("Allow"); strings.Join(got, ",") != strings.Join(tt.allow, ",") {
				t.Errorf("Allow = %v, want %v", got, tt.allow)
			}
		})
	}
}

func TestModuleRoutesAreMountedUnderAPIPrefix(t *testing.T) {
	h := newHarness(t, usersModule)

	if rec := h.do(http.MethodGet, "/api/v1/users/7", nil); rec.Code != http.StatusNoContent {
		t.Errorf("GET /api/v1/users/7 status = %d, want 204", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/users/7", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /users/7 status = %d, want 404", rec.Code)
	}
}

func TestRequestIDAcceptsValidIncomingID(t *testing.T) {
	h := newHarness(t, usersModule)
	id := "abc-123_DEF.456:789"

	rec := h.do(http.MethodGet, "/api/v1/users/1", http.Header{"X-Request-Id": {id}})

	if got := rec.Header().Get(httpx.RequestIDHeader); got != id {
		t.Errorf("response X-Request-ID = %q, want %q", got, id)
	}
	logs := h.records(t, "http request")
	if len(logs) != 1 || logs[0]["request_id"] != id {
		t.Errorf("access log = %v, want request_id %q", logs, id)
	}
}

func TestRequestIDRegeneratesMissingOrInvalidID(t *testing.T) {
	h := newHarness(t)
	cases := map[string]http.Header{
		"missing":  nil,
		"empty":    {"X-Request-Id": {""}},
		"too long": {"X-Request-Id": {strings.Repeat("a", httpx.MaxRequestIDLength+1)}},
		"unsafe":   {"X-Request-Id": {"abc\ndef"}},
		"spaces":   {"X-Request-Id": {"has space"}},
		"injected": {"X-Request-Id": {`"},{"evil":"x`}},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			rec := h.do(http.MethodGet, "/health/live", header)
			got := rec.Header().Get(httpx.RequestIDHeader)
			if !httpx.ValidRequestID(got) || len(got) != 32 {
				t.Errorf("generated X-Request-ID = %q, want 32 hex chars", got)
			}
			if header != nil && got == header.Get("X-Request-Id") {
				t.Errorf("invalid ID %q was accepted", got)
			}
		})
	}
}

func TestRequestIDAcceptsMaxLength(t *testing.T) {
	id := strings.Repeat("x", httpx.MaxRequestIDLength)
	if !httpx.ValidRequestID(id) {
		t.Errorf("ValidRequestID(%d chars) = false, want true", len(id))
	}
}

func TestRequestIDPropagatesToHandlerContext(t *testing.T) {
	var seen string
	h := newHarness(t, httpx.ModuleFunc(func(r chi.Router) {
		r.Get("/echo", func(w http.ResponseWriter, r *http.Request) {
			seen = httpx.RequestIDFrom(r)
		})
	}))

	rec := h.do(http.MethodGet, "/api/v1/echo", http.Header{"X-Request-Id": {"ctx-id"}})

	if seen != "ctx-id" || rec.Header().Get(httpx.RequestIDHeader) != "ctx-id" {
		t.Errorf("handler saw %q, response had %q; want ctx-id", seen, rec.Header().Get(httpx.RequestIDHeader))
	}
}

func TestRecovererReturnsProblemAndLogsPanic(t *testing.T) {
	h := newHarness(t, usersModule)

	rec := h.do(http.MethodGet, "/api/v1/boom", http.Header{"X-Request-Id": {"panic-1"}})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	p := decodeProblem(t, rec)
	if p.Code != httpx.CodeInternal || p.Status != 500 {
		t.Errorf("problem = %+v", p)
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "goroutine") {
		t.Errorf("response leaks internals: %s", rec.Body.String())
	}

	panics := h.records(t, "panic recovered")
	if len(panics) != 1 {
		t.Fatalf("got %d panic log records, want 1", len(panics))
	}
	pr := panics[0]
	if pr["level"] != "ERROR" || pr["panic"] != "secret internal detail" || pr["request_id"] != "panic-1" {
		t.Errorf("panic log = %v", pr)
	}
	if stack, _ := pr["stack"].(string); !strings.Contains(stack, "goroutine") {
		t.Errorf("panic log has no stack trace: %v", pr["stack"])
	}

	if got := h.obs.last(t); got != (observation{"GET", "/api/v1/boom", 500}) {
		t.Errorf("metrics observed %+v, want recovered 500", got)
	}
	if logs := h.records(t, "http request"); len(logs) != 1 || logs[0]["status"] != float64(500) {
		t.Errorf("access log = %v, want status 500", logs)
	}
}

func TestRecovererKeepsCommittedResponse(t *testing.T) {
	h := newHarness(t, usersModule)

	rec := h.do(http.MethodGet, "/api/v1/boom-after-write", nil)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want the already-committed 202", rec.Code)
	}
	panics := h.records(t, "panic recovered")
	if len(panics) != 1 || panics[0]["response_committed"] != true {
		t.Errorf("panic log = %v, want response_committed=true", panics)
	}
}

func TestServerKeepsServingAfterPanic(t *testing.T) {
	h := newHarness(t, usersModule)
	srv := httptest.NewServer(h.handler)
	defer srv.Close()

	for i, want := range []int{500, 200} {
		path := []string{"/api/v1/boom", "/health/live"}[i]
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s status = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestAccessLogUsesRouteTemplate(t *testing.T) {
	h := newHarness(t, usersModule)

	h.do(http.MethodGet, "/api/v1/users/secret-token-123?email=a@b.c", http.Header{"X-Request-Id": {"log-1"}})

	logs := h.records(t, "http request")
	if len(logs) != 1 {
		t.Fatalf("got %d access logs, want 1", len(logs))
	}
	l := logs[0]
	want := map[string]any{
		"level": "INFO", "method": "GET", "route": "/api/v1/users/{id}",
		"status": float64(204), "request_id": "log-1", "service": "api",
	}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("access log %s = %v, want %v", k, l[k], v)
		}
	}
	if _, ok := l["duration_ms"]; !ok {
		t.Error("access log has no duration")
	}
	if strings.Contains(h.logs.String(), "secret-token-123") || strings.Contains(h.logs.String(), "a@b.c") {
		t.Errorf("access log leaks raw path or query: %s", h.logs.String())
	}
}

func TestAccessLogProbesAtDebugAndUnmatchedLabel(t *testing.T) {
	h := newHarness(t)
	h.do(http.MethodGet, "/health/live", nil)
	h.do(http.MethodGet, "/some/random/path", nil)

	logs := h.records(t, "http request")
	if len(logs) != 2 {
		t.Fatalf("got %d access logs, want 2", len(logs))
	}
	if logs[0]["level"] != "DEBUG" || logs[0]["route"] != "/health/live" {
		t.Errorf("probe access log = %v, want DEBUG /health/live", logs[0])
	}
	if logs[1]["route"] != httpx.UnmatchedRoute || logs[1]["status"] != float64(404) {
		t.Errorf("unmatched access log = %v", logs[1])
	}
}

func TestMetricsUseBoundedRouteLabels(t *testing.T) {
	h := newHarness(t, usersModule)

	h.do(http.MethodGet, "/api/v1/users/1", nil)
	h.do(http.MethodGet, "/api/v1/users/2", nil)
	h.do(http.MethodGet, "/random/abc", nil)
	h.do(http.MethodGet, "/api/v1/random/xyz", nil)
	h.do(http.MethodPatch, "/api/v1/users", nil)

	want := []observation{
		{"GET", "/api/v1/users/{id}", 204},
		{"GET", "/api/v1/users/{id}", 204},
		{"GET", httpx.UnmatchedRoute, 404},
		{"GET", httpx.UnmatchedRoute, 404},
		// chi records no route pattern when only the method fails to match.
		{"PATCH", httpx.UnmatchedRoute, 405},
	}
	if len(h.obs.seen) != len(want) {
		t.Fatalf("observed %v, want %v", h.obs.seen, want)
	}
	for i := range want {
		if h.obs.seen[i] != want[i] {
			t.Errorf("observation %d = %+v, want %+v", i, h.obs.seen[i], want[i])
		}
	}
}
