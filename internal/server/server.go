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

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

const (
	// shutdownTimeout bounds how long in-flight requests get to finish on
	// shutdown.
	shutdownTimeout = 10 * time.Second
	// writeTimeout bounds writing a response. Long-lived responses such as
	// the SSE stream extend it per write with http.ResponseController.
	writeTimeout = 30 * time.Second
)

// SnapshotSource provides the latest metrics snapshot and a feed of new ones.
// *metrics.Sampler implements it.
type SnapshotSource interface {
	Latest() (metrics.Snapshot, bool)
	// Subscribe returns a channel of new snapshots and a function that
	// unsubscribes and closes it. The channel is also closed when the source
	// stops.
	Subscribe() (<-chan metrics.Snapshot, func())
}

// Server serves the pisens HTTP API.
type Server struct {
	addr     string
	version  string
	logger   *slog.Logger
	mux      *http.ServeMux
	snapshot SnapshotSource

	// Timeouts are fields so tests can shorten them.
	writeTimeout       time.Duration
	heartbeatInterval  time.Duration
	streamWriteTimeout time.Duration
}

// New creates a server that listens on addr, reports version from the health
// endpoint and serves metrics from snapshots.
func New(addr, version string, logger *slog.Logger, snapshots SnapshotSource) *Server {
	s := &Server{
		addr:     addr,
		version:  version,
		logger:   logger,
		mux:      http.NewServeMux(),
		snapshot: snapshots,

		writeTimeout:       writeTimeout,
		heartbeatInterval:  heartbeatInterval,
		streamWriteTimeout: streamWriteTimeout,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /api/stream", s.handleStream)
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
		WriteTimeout:      s.writeTimeout,
		// Request contexts derive from ctx, so long-lived handlers such as
		// the SSE stream return as soon as shutdown starts instead of
		// holding Shutdown up until shutdownTimeout.
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
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

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	snap, ok := s.snapshot.Latest()
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "no metrics sampled yet"})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
