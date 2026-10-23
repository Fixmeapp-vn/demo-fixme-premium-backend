// Command worker runs background jobs. No jobs are registered yet; event
// dispatch and outbox processing arrive in later stories.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/config"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
)

const serviceName = "worker"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.LookupEnv, os.Stdout, os.Stderr, nil))
}

// run starts jobs and blocks until ctx is cancelled. It returns the process
// exit code.
func run(ctx context.Context, lookup config.LookupFunc, stdout, stderr io.Writer, jobs []Job) int {
	cfg, err := config.Load(lookup)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", serviceName, err)
		return 1
	}

	logger := logging.New(stdout, logging.Options{
		Service: serviceName,
		Env:     string(cfg.AppEnv),
		Level:   cfg.LogLevel,
	})
	logger.Info("starting", slog.Any("config", cfg), slog.Int("jobs", len(jobs)))

	if err := NewRunner(logger, cfg.ShutdownTimeout, jobs...).Run(ctx); err != nil {
		logger.Error("stopped with error", slog.Any("error", err))
		return 1
	}
	logger.Info("stopped")
	return 0
}
