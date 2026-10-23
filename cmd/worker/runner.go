package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Job is a long-running background task. Run must return promptly once ctx
// is cancelled.
type Job interface {
	Name() string
	Run(ctx context.Context) error
}

// Runner runs jobs concurrently until its context is cancelled, then waits
// up to the shutdown timeout for them to return.
type Runner struct {
	logger          *slog.Logger
	shutdownTimeout time.Duration
	jobs            []Job
}

func NewRunner(logger *slog.Logger, shutdownTimeout time.Duration, jobs ...Job) *Runner {
	return &Runner{logger: logger, shutdownTimeout: shutdownTimeout, jobs: jobs}
}

// Run blocks until ctx is cancelled or a job fails. A job failure cancels
// the remaining jobs. It returns an error if any job failed or if the jobs
// did not stop within the shutdown timeout.
func (r *Runner) Run(ctx context.Context) error {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		jobErrs []error
	)
	for _, j := range r.jobs {
		wg.Add(1)
		go func(j Job) {
			defer wg.Done()
			if err := j.Run(jobCtx); err != nil && !errors.Is(err, context.Canceled) {
				mu.Lock()
				jobErrs = append(jobErrs, fmt.Errorf("job %s: %w", j.Name(), err))
				mu.Unlock()
				cancel()
			}
		}(j)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	r.logger.Info("worker started")

	// Jobs that return early without error leave the worker idle until
	// shutdown rather than terminating the process.
	<-jobCtx.Done()

	r.logger.Info("worker shutdown started", slog.String("timeout", r.shutdownTimeout.String()))
	timer := time.NewTimer(r.shutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		r.logger.Error("worker shutdown deadline exceeded", slog.String("timeout", r.shutdownTimeout.String()))
		return fmt.Errorf("worker: jobs did not stop within %s", r.shutdownTimeout)
	}

	mu.Lock()
	defer mu.Unlock()
	if err := errors.Join(jobErrs...); err != nil {
		return err
	}
	r.logger.Info("worker shutdown complete")
	return nil
}
