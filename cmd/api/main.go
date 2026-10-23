// Command api runs the public HTTP API and the internal metrics listener.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/config"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/httpx"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/observability"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/postgres"
)

const serviceName = "api"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.LookupEnv, os.Stdout, os.Stderr))
}

// run starts both listeners and blocks until ctx is cancelled or a listener
// fails. It returns the process exit code.
func run(ctx context.Context, lookup config.LookupFunc, stdout, stderr io.Writer) int {
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
	logger.Info("starting", slog.Any("config", cfg))

	metrics := observability.NewMetrics()

	// The runtime (user_runtime) pool is optional: when no database is
	// configured the API starts without a readiness check. Migrations are
	// never applied here; that is cmd/migrate's job as user_migrate. The
	// pool is lazy, so the API still starts while Postgres is down and
	// readiness flips to healthy once it is reachable.
	var readinessChecks []httpx.HealthCheck
	if cfg.UserDatabaseURL != "" {
		pool, err := postgres.Open(ctx, cfg.UserDatabaseURL)
		if err != nil {
			logger.Error("failed to open user database pool", slog.Any("error", err))
			return 1
		}
		defer pool.Close()
		readinessChecks = append(readinessChecks, postgres.NewCheck("user_db", pool, 0))
		logger.Info("user database configured")
	}

	api := httpx.NewServer(httpx.ServerConfig{
		Name:              "api",
		Addr:              cfg.HTTP.Addr,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ShutdownTimeout:   cfg.ShutdownTimeout,
	}, httpx.NewRouter(httpx.RouterDeps{Logger: logger, Observer: metrics, ReadinessChecks: readinessChecks}), logger)

	internal := httpx.NewServer(httpx.ServerConfig{
		Name:              "metrics",
		Addr:              cfg.MetricsAddr,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ShutdownTimeout:   cfg.ShutdownTimeout,
	}, metrics.Handler(), logger)

	// If either listener fails, gctx is cancelled and the other shuts down.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return api.ListenAndServe(gctx) })
	g.Go(func() error { return internal.ListenAndServe(gctx) })

	if err := g.Wait(); err != nil {
		logger.Error("stopped with error", slog.Any("error", err))
		return 1
	}
	logger.Info("stopped")
	return 0
}
