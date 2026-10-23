package httpx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/httpx"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
)

type fakeCheck struct {
	name string
	err  error
}

func (c fakeCheck) Name() string                { return c.name }
func (c fakeCheck) Check(context.Context) error { return c.err }

func readinessHandler(checks ...httpx.ReadinessChecker) http.Handler {
	return httpx.NewRouter(httpx.RouterDeps{
		Logger:    logging.Discard(),
		Observer:  &fakeObserver{},
		Readiness: checks,
	})
}

func doReq(h http.Handler, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReadinessAllChecksPass(t *testing.T) {
	h := readinessHandler(fakeCheck{name: "user_db"})
	rec := doReq(h, http.MethodGet, httpx.ReadinessPath)

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

func TestReadinessNoChecksReportsReady(t *testing.T) {
	h := readinessHandler()
	rec := doReq(h, http.MethodGet, httpx.ReadinessPath)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReadinessFailingCheckReturns503(t *testing.T) {
	h := readinessHandler(
		fakeCheck{name: "user_db", err: errors.New("connection refused")},
		fakeCheck{name: "cache"},
	)
	rec := doReq(h, http.MethodGet, httpx.ReadinessPath)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"unavailable"`) {
		t.Errorf("body = %s, want status unavailable", body)
	}
	if !strings.Contains(body, "user_db") {
		t.Errorf("body = %s, want it to name the failing check", body)
	}
}

func TestLivenessIndependentOfReadiness(t *testing.T) {
	h := readinessHandler(fakeCheck{name: "user_db", err: errors.New("down")})

	if rec := doReq(h, http.MethodGet, httpx.ReadinessPath); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", rec.Code)
	}
	rec := doReq(h, http.MethodGet, httpx.LivenessPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want 200 even when readiness fails", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"ok"}` {
		t.Errorf("liveness body = %s", body)
	}
}
