package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ServerConfig holds the listener settings of a Server.
type ServerConfig struct {
	Name              string
	Addr              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// Server is an http.Server with a context-driven graceful-shutdown lifecycle.
type Server struct {
	cfg    ServerConfig
	srv    *http.Server
	logger *slog.Logger
}

// NewServer creates a Server serving h. It does not start listening.
func NewServer(cfg ServerConfig, h http.Handler, logger *slog.Logger) *Server {
	logger = logger.With(slog.String("server", cfg.Name))
	return &Server{
		cfg:    cfg,
		logger: logger,
		srv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           h,
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		},
	}
}

// ListenAndServe listens on the configured address and calls Serve.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("%s server: listen on %s: %w", s.cfg.Name, s.cfg.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve accepts connections on ln until ctx is cancelled, then shuts down
// gracefully: the listener is closed and in-flight requests get up to
// ShutdownTimeout to finish. Requests still running at the deadline have
// their contexts cancelled and their connections closed, and an error is
// returned. Serve returns nil after a clean shutdown.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	// Request contexts derive from baseCtx rather than ctx so that a shutdown
	// signal does not cancel in-flight requests before the deadline.
	baseCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	s.srv.BaseContext = func(net.Listener) context.Context { return baseCtx }

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.srv.Serve(ln) }()
	s.logger.Info("server started", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("%s server: %w", s.cfg.Name, err)
	case <-ctx.Done():
	}

	s.logger.Info("server shutdown started", slog.String("timeout", s.cfg.ShutdownTimeout.String()))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("server shutdown deadline exceeded; cancelling in-flight requests",
			slog.String("timeout", s.cfg.ShutdownTimeout.String()), slog.Any("error", err))
		cancelRequests()
		_ = s.srv.Close()
		<-serveErr
		return fmt.Errorf("%s server: graceful shutdown: %w", s.cfg.Name, err)
	}

	<-serveErr
	s.logger.Info("server shutdown complete")
	return nil
}
