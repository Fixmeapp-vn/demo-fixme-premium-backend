package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
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

type funcJob struct {
	name string
	run  func(ctx context.Context) error
}

func (j funcJob) Name() string                  { return j.name }
func (j funcJob) Run(ctx context.Context) error { return j.run(ctx) }

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestRunWithoutJobsWaitsForShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var stdout syncBuffer
	code := make(chan int, 1)
	go func() { code <- run(ctx, env(nil), &stdout, &stdout, nil) }()

	select {
	case c := <-code:
		t.Fatalf("run exited early with %d", c)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()

	select {
	case c := <-code:
		if c != 0 {
			t.Errorf("exit code = %d, want 0", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancellation")
	}
	for _, msg := range []string{`"msg":"worker started"`, `"msg":"worker shutdown started"`, `"msg":"worker shutdown complete"`, `"service":"worker"`} {
		if !strings.Contains(stdout.String(), msg) {
			t.Errorf("logs missing %s:\n%s", msg, stdout.String())
		}
	}
}

func TestRunInvalidConfigExitsNonZero(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(context.Background(), env(map[string]string{"APP_ENV": "nope"}), &bytes.Buffer{}, &stderr, nil); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "APP_ENV") {
		t.Errorf("stderr = %q, want it to mention APP_ENV", stderr.String())
	}
}

func TestRunnerStopsJobsGracefully(t *testing.T) {
	stopped := make(chan struct{})
	job := funcJob{"tick", func(ctx context.Context) error {
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- NewRunner(logging.Discard(), time.Second, job).Run(ctx) }()

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("Run() = %v, want nil", err)
	}
	select {
	case <-stopped:
	default:
		t.Error("job did not observe cancellation")
	}
}

func TestRunnerShutdownTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	stuck := funcJob{"stuck", func(context.Context) error { <-release; return nil }}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	begin := time.Now()
	err := NewRunner(logging.Discard(), 150*time.Millisecond, stuck).Run(ctx)

	if err == nil || !strings.Contains(err.Error(), "did not stop within") {
		t.Errorf("Run() = %v, want shutdown timeout error", err)
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Errorf("Run took %s, want about 150ms", elapsed)
	}
}

func TestRunnerJobFailureStopsOtherJobs(t *testing.T) {
	boom := errors.New("boom")
	failing := funcJob{"failing", func(context.Context) error { return boom }}
	waiting := funcJob{"waiting", func(ctx context.Context) error { <-ctx.Done(); return nil }}

	err := NewRunner(logging.Discard(), time.Second, failing, waiting).Run(context.Background())
	if !errors.Is(err, boom) {
		t.Errorf("Run() = %v, want boom", err)
	}
}
