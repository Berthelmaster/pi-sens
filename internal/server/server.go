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
	"github.com/Berthelmaster/pi-sens/internal/auth"
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
	// healthPath is the health endpoint, which basic auth leaves open.
	healthPath = "/api/health"
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
	DailyMax(ctx context.Context, key string, from, to time.Time, loc *time.Location) ([]history.DayMax, error)
}

// Server serves the pisens HTTP API and the web UI.
type Server struct {
	addr     string
	version  string
	webBuild string // build ID of the frontend in ui, "" without one
	ui       fs.FS
	logger   *slog.Logger
	mux      *http.ServeMux
	handler  http.Handler // mux, behind basic auth when that is on
	auth     *auth.Basic  // nil leaves every route open
	snapshot SnapshotSource
	alerts   AlertSource      // nil turns off /api/alerts and alert events
	history  HistorySource    // nil turns off /api/series, /api/history and /api/energy/daily
	now      func() time.Time // the default end of a history query; tests fix it
	loc      *time.Location   // whose midnight starts a day in /api/energy/daily
	// energyPrice per kWh, above zero, adds costs in energyCurrency to
	// /api/energy/daily. WithEnergyPrice sets them.
	energyPrice    float64
	energyCurrency string
	// prometheus turns on /metrics. EnablePrometheus sets it before serving.
	prometheus bool

	// Timeouts are fields so tests can shorten them.
	writeTimeout       time.Duration
	heartbeatInterval  time.Duration
	streamWriteTimeout time.Duration
}

// Option configures optional server behaviour in New.
type Option func(*Server)

// WithBasicAuth requires HTTP basic auth checked by b on every route except
// /api/health, which stays open for uptime checks and monitoring. A nil
// b leaves the server open, like leaving the option out.
func WithBasicAuth(b *auth.Basic) Option {
	return func(s *Server) { s.auth = b }
}

// WithEnergyPrice sets the electricity price per kWh, and the currency to
// label it with, that /api/energy/daily prices each day's use at. A price of
// zero or less leaves the costs out.
func WithEnergyPrice(price float64, currency string) Option {
	return func(s *Server) { s.energyPrice, s.energyCurrency = price, currency }
}

// New creates a server that listens on addr, reports version and the build ID
// of the frontend in ui from the health endpoint, serves metrics from
// snapshots and alerts from alertSource, and serves the single-page app in ui
// at /. ui is normally web.Dist().
// alertSource may be nil, which leaves out /api/alerts and the alert events.
// historySource may be nil, which leaves out /api/series, /api/history and
// /api/energy/daily.
func New(addr, version string, ui fs.FS, logger *slog.Logger, snapshots SnapshotSource, alertSource AlertSource,
	historySource HistorySource, opts ...Option) *Server {
	s := &Server{
		addr:     addr,
		version:  version,
		webBuild: webBuild(ui),
		ui:       ui,
		logger:   logger,
		mux:      http.NewServeMux(),
		snapshot: snapshots,
		alerts:   alertSource,
		history:  historySource,
		now:      time.Now,
		loc:      time.Local,

		writeTimeout:       writeTimeout,
		heartbeatInterval:  heartbeatInterval,
		streamWriteTimeout: streamWriteTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.routes()
	s.handler = s.mux
	if s.auth != nil {
		s.handler = s.auth.Middleware(s.mux, healthPath)
	}
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET "+healthPath, s.handleHealth)
	s.mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /api/stream", s.handleStream)
	if s.alerts != nil {
		s.mux.HandleFunc("GET /api/alerts", s.handleAlerts)
	}
	if s.history != nil {
		s.mux.HandleFunc("GET /api/series", s.handleSeries)
		s.mux.HandleFunc("GET /api/history", s.handleHistory)
		s.mux.HandleFunc("GET /api/energy/daily", s.handleEnergyDaily)
	}
	s.mux.HandleFunc("GET /metrics", s.handlePrometheus)
	// Everything else, including unknown /api paths, goes to the SPA handler.
	s.mux.Handle("GET /", &spaHandler{ui: s.ui, logger: s.logger})
}

// Handler returns the server's root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.handler
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
		Handler:           s.handler,
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
	// Web is the build ID of the embedded frontend. An open page compares it
	// with its own to offer a reload after a deploy. Left out when the
	// frontend has none.
	Web string `json:"web,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: s.version, Web: s.webBuild})
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
	_ = encodeJSON(w, v)
}

// encodeJSON writes v as JSON followed by a newline, without HTML escaping,
// so an alert's op reads ">" rather than "\u003e".
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
