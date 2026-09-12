package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/config"
)

// shutdownTimeout bounds how long in-flight requests may finish after the
// context passed to Run is cancelled.
const shutdownTimeout = 10 * time.Second

// Options configures a Server beyond what config.Config carries.
type Options struct {
	// WebDir is the directory holding the built frontend. When empty the
	// server serves the API only, which is what `make dev` wants because Vite
	// serves the frontend itself.
	WebDir string
}

// Server serves the Eika HTTP API.
type Server struct {
	cfg  config.Config
	log  *slog.Logger
	mux  *http.ServeMux
	opts Options
}

// New builds a Server for the given configuration.
func New(cfg config.Config, log *slog.Logger, opts Options) *Server {
	s := &Server{cfg: cfg, log: log, mux: http.NewServeMux(), opts: opts}
	s.routes()
	return s
}

// Handler returns the server's root handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Run serves until ctx is cancelled, then drains in-flight requests.
func (s *Server) Run(ctx context.Context) error {
	return Serve(ctx, s.cfg.Listen, s.mux, s.log)
}

// Serve listens on addr and serves h until ctx is cancelled, then drains
// in-flight requests. It returns nil on a clean shutdown.
func Serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		defer close(serveErr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	log.Info("http server listening", "addr", ln.Addr().String())

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve on %s: %w", addr, err)
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down server on %s: %w", addr, err)
	}
	log.Info("http server stopped", "addr", addr)
	return nil
}
