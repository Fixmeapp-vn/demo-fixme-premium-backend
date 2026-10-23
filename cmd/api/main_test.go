package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	var lastErr error
	for i := 0; i < 50; i++ {
		resp, err := http.Get(url)
		if err == nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET %s: %v", url, lastErr)
	return 0, ""
}

func TestRunServesAPIAndMetricsOnSeparateListeners(t *testing.T) {
	apiAddr, metricsAddr := freeAddr(t), freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout syncBuffer
	code := make(chan int, 1)
	go func() {
		code <- run(ctx, env(map[string]string{
			"APP_ENV": "test", "HTTP_ADDR": apiAddr, "METRICS_ADDR": metricsAddr, "SHUTDOWN_TIMEOUT": "2s",
		}), &stdout, &stdout)
	}()

	if status, body := get(t, "http://"+apiAddr+"/health/live"); status != 200 || !strings.Contains(body, `"ok"`) {
		t.Errorf("liveness = %d %s", status, body)
	}
	if status, _ := get(t, "http://"+apiAddr+"/metrics"); status != 404 {
		t.Errorf("public /metrics status = %d, want 404", status)
	}
	status, body := get(t, "http://"+metricsAddr+"/metrics")
	if status != 200 || !strings.Contains(body, `http_requests_total{method="GET",route="/health/live",status="200"} 1`) {
		t.Errorf("internal /metrics = %d, missing liveness counter:\n%s", status, body)
	}

	cancel()
	select {
	case c := <-code:
		if c != 0 {
			t.Errorf("exit code = %d, want 0", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
	for _, s := range []string{`"msg":"server shutdown complete","service":"api","env":"test","server":"api"`, `"server":"metrics"`} {
		if !strings.Contains(stdout.String(), s) {
			t.Errorf("logs missing %s:\n%s", s, stdout.String())
		}
	}
}

func TestRunInvalidConfigExitsNonZero(t *testing.T) {
	var stderr bytes.Buffer
	code := run(context.Background(), env(map[string]string{"HTTP_ADDR": "", "LOG_LEVEL": "loud"}), io.Discard, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "HTTP_ADDR") || !strings.Contains(stderr.String(), "LOG_LEVEL") {
		t.Errorf("stderr = %q, want both invalid fields reported", stderr.String())
	}
}

func TestRunStartupFailureExitsNonZero(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	code := run(context.Background(), env(map[string]string{
		"HTTP_ADDR": ln.Addr().String(), "METRICS_ADDR": freeAddr(t),
	}), io.Discard, io.Discard)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 when the API port is busy", code)
	}
}
