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
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/database"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/httpx"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/logging"
	"github.com/fixmeapp-vn/demo-fixme-premium-backend/internal/platform/observability"
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

	// Readiness depends on the User module database. When its runtime DSN is
	// not configured, readiness reports not-ready rather than silently passing;
	// liveness stays independent of the database either way.
	var userDBCheck httpx.ReadinessChecker
	if cfg.UserDatabaseURL == "" {
		logger.Warn("USER_DATABASE_URL is not set; /health/ready will report not-ready")
		userDBCheck = database.NewUnavailableChecker("user_db", "USER_DATABASE_URL not configured")
	} else {
		pool, err := database.NewPool(ctx, cfg.UserDatabaseURL)
		if err != nil {
			logger.Error("could not create user database pool", slog.Any("error", err))
			return 1
		}
		defer pool.Close()
		userDBCheck = database.NewPingChecker("user_db", pool)
	}

	api := httpx.NewServer(httpx.ServerConfig{
		Name:              "api",
		Addr:              cfg.HTTP.Addr,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ShutdownTimeout:   cfg.ShutdownTimeout,
	}, httpx.NewRouter(httpx.RouterDeps{
		Logger:    logger,
		Observer:  metrics,
		Readiness: []httpx.ReadinessChecker{userDBCheck},
	}), logger)

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
