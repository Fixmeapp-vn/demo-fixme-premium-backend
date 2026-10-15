package httpx_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/httpx"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
)

func startServer(t *testing.T, shutdownTimeout time.Duration, h http.Handler) (url string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httpx.NewServer(httpx.ServerConfig{
		Name:              "test",
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       5 * time.Second,
		ShutdownTimeout:   shutdownTimeout,
	}, h, logging.Discard())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx, ln) }()
	t.Cleanup(cancel)
	return "http://" + ln.Addr().String(), cancel, errCh
}

func waitErr(t *testing.T, done <-chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		t.Fatalf("Serve did not return within %s", within)
		return nil
	}
}

func TestServerShutdownWaitsForInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "finished")
	})
	url, cancel, done := startServer(t, 5*time.Second, h)

	type result struct {
		body string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		resCh <- result{string(b), err}
	}()

	<-started
	cancel()

	// New connections are refused once shutdown has begun.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", url[len("http://"):], 100*time.Millisecond)
		if err != nil {
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting connections after shutdown started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case err := <-done:
		t.Fatalf("Serve returned %v before the in-flight request finished", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	res := <-resCh
	if res.err != nil || res.body != "finished" {
		t.Fatalf("in-flight request = %q, %v; want finished", res.body, res.err)
	}
	if err := waitErr(t, done, 2*time.Second); err != nil {
		t.Errorf("Serve() = %v, want nil after clean shutdown", err)
	}
}

func TestServerShutdownTimeoutCancelsRequests(t *testing.T) {
	started := make(chan struct{})
	reqCancelled := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(reqCancelled)
	})
	url, cancel, done := startServer(t, 200*time.Millisecond, h)

	go func() {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	begin := time.Now()
	cancel()
	err := waitErr(t, done, 2*time.Second)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Serve() = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(begin); elapsed < 200*time.Millisecond || elapsed > time.Second {
		t.Errorf("shutdown took %s, want about the 200ms deadline", elapsed)
	}
	select {
	case <-reqCancelled:
	case <-time.After(time.Second):
		t.Error("in-flight request context was not cancelled after the deadline")
	}
}

func TestServerCancelsRequestContextOnClientDisconnect(t *testing.T) {
	started := make(chan struct{})
	reqCancelled := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(reqCancelled)
	})
	url, _, _ := startServer(t, time.Second, h)

	ctx, abort := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	abort()

	select {
	case <-reqCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("request context not cancelled after client disconnect")
	}
}

func TestListenAndServeReportsListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := httpx.NewServer(httpx.ServerConfig{Name: "test", Addr: ln.Addr().String(), ShutdownTimeout: time.Second},
		http.NotFoundHandler(), logging.Discard())
	if err := srv.ListenAndServe(context.Background()); err == nil {
		t.Fatal("ListenAndServe() on a busy port = nil, want error")
	}
}
