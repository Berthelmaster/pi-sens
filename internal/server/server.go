// Package server is the pisens HTTP server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
	"github.com/Berthelmaster/pi-sens/internal/history"
	"github.com/Berthelmaster/pi-sens/internal/metrics"
	"github.com/Berthelmaster/pi-sens/internal/promtext"
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

// AlertSource provides the current alerts and a feed of changes to them.
// *alerts.Evaluator implements it.
type AlertSource interface {
	Alerts() alerts.Set
	// Subscribe returns a channel that receives the new set whenever it
	// changes and a function that unsubscribes and closes it. The channel is
	// also closed when the source stops.
	Subscribe() (<-chan alerts.Set, func())
}

// HistorySource lists the stored series and reads their history.
// *history.Store implements it.
type HistorySource interface {
	SeriesKeys(ctx context.Context, prefix string) ([]string, error)
	Query(ctx context.Context, q history.Query) (history.Result, error)
}

// Server serves the pisens HTTP API and the web UI.
type Server struct {
	addr     string
	version  string
	ui       fs.FS
	logger   *slog.Logger
	mux      *http.ServeMux
	snapshot SnapshotSource
	alerts   AlertSource      // nil turns off /api/alerts and alert events
	history  HistorySource    // nil turns off /api/series and /api/history
	now      func() time.Time // the default end of a history query; tests fix it
	// prometheus turns on /metrics. EnablePrometheus sets it before serving.
	prometheus bool

	// Timeouts are fields so tests can shorten them.
	writeTimeout       time.Duration
	heartbeatInterval  time.Duration
	streamWriteTimeout time.Duration
}

// New creates a server that listens on addr, reports version from the health
// endpoint, serves metrics from snapshots and alerts from alertSource, and
// serves the single-page app in ui at /. ui is normally web.Dist().
// alertSource may be nil, which leaves out /api/alerts and the alert events.
// historySource may be nil, which leaves out /api/series and /api/history.
func New(addr, version string, ui fs.FS, logger *slog.Logger, snapshots SnapshotSource, alertSource AlertSource,
	historySource HistorySource) *Server {
	s := &Server{
		addr:     addr,
		version:  version,
		ui:       ui,
		logger:   logger,
		mux:      http.NewServeMux(),
		snapshot: snapshots,
		alerts:   alertSource,
		history:  historySource,
		now:      time.Now,

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
	if s.alerts != nil {
		s.mux.HandleFunc("GET /api/alerts", s.handleAlerts)
	}
	if s.history != nil {
		s.mux.HandleFunc("GET /api/series", s.handleSeries)
		s.mux.HandleFunc("GET /api/history", s.handleHistory)
	}
	s.mux.HandleFunc("GET /metrics", s.handlePrometheus)
	// Everything else, including unknown /api paths, goes to the SPA handler.
	s.mux.Handle("GET /", spaHandler{ui: s.ui, logger: s.logger})
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

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: s.version})
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

// EnablePrometheus turns on GET /metrics, which serves the latest snapshot's
// series in the Prometheus text format. Call it before Run or Serve.
func (s *Server) EnablePrometheus() {
	s.prometheus = true
}

func (s *Server) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	if !s.prometheus {
		// Turned off: a plain 404 rather than the SPA page, so a scraper
		// that is still pointed here fails clearly.
		http.NotFound(w, r)
		return
	}
	snap, ok := s.snapshot.Latest()
	if !ok {
		// Prometheus records a failed scrape, which is what this is.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "no metrics sampled yet\n")
		return
	}
	w.Header().Set("Content-Type", promtext.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	if err := promtext.Write(w, snap); err != nil {
		s.logger.Debug("write prometheus metrics", "err", err)
	}
}

func (s *Server) handleAlerts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.alerts.Alerts())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
