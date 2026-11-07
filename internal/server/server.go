// Package server is the pisens HTTP server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// shutdownTimeout bounds how long in-flight requests get to finish on shutdown.
const shutdownTimeout = 10 * time.Second

// Server serves the pisens HTTP API.
type Server struct {
	addr    string
	version string
	logger  *slog.Logger
	mux     *http.ServeMux
}

// New creates a server that listens on addr and reports version from the
// health endpoint.
func New(addr, version string, logger *slog.Logger) *Server {
	s := &Server{
		addr:    addr,
		version: version,
		logger:  logger,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
}

// Handler returns the server's root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Run listens on the configured address and serves until ctx is cancelled,
// then shuts down gracefully. It returns nil after a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve is like Run but uses an existing listener.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
	}

	errc := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", ln.Addr().String())
		errc <- srv.Serve(ln)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	s.logger.Info("http server shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: s.version})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
