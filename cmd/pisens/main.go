// Command pisens serves Raspberry Pi system metrics over HTTP.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
	"github.com/Berthelmaster/pi-sens/internal/auth"
	"github.com/Berthelmaster/pi-sens/internal/collectors" // also registers the collectors
	"github.com/Berthelmaster/pi-sens/internal/config"
	"github.com/Berthelmaster/pi-sens/internal/energy"
	"github.com/Berthelmaster/pi-sens/internal/history"
	"github.com/Berthelmaster/pi-sens/internal/metrics"
	"github.com/Berthelmaster/pi-sens/internal/notify"
	"github.com/Berthelmaster/pi-sens/internal/server"
	"github.com/Berthelmaster/pi-sens/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		os.Exit(runHashPassword(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(run(os.Args[1:], os.Getenv, os.Stderr))
}

// run starts pisens with the given arguments (without the program name) and
// environment, logs to stderr and returns the exit code.
func run(args []string, getenv func(string) string, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(args, getenv, stderr)
	if errors.Is(err, config.ErrHelp) {
		return 0
	}
	if errors.Is(err, config.ErrVersion) {
		fmt.Println("pisens", version)
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "pisens:", err)
		return 2
	}
	// A broken rules file stops startup rather than leaving the Pi silently
	// unmonitored.
	rules, err := alerts.LoadRules(cfg.AlertsFile)
	if err != nil {
		fmt.Fprintln(stderr, "pisens:", err)
		return 2
	}

	// ctx is cancelled on SIGINT/SIGTERM. Background workers started here
	// should take it and stop when it is done.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting pisens",
		"version", version,
		"listen", cfg.Listen,
		"interval", cfg.Interval.String(),
		"data_dir", cfg.DataDir,
		"history_interval", cfg.HistoryInterval.String(),
		"history_retention", fmt.Sprintf("raw %s, 1m %s, 15m %s",
			cfg.HistoryRawRetention, cfg.History1mRetention, cfg.History15mRetention),
		"watch_services", strings.Join(cfg.WatchServices, ","),
		"process_cmdline", cfg.ProcessCmdline,
		"net_skip", strings.Join(cfg.NetSkip, ","),
	)
	rulesFrom := "built-in"
	if cfg.AlertsFile != "" {
		rulesFrom = cfg.AlertsFile
	}
	logger.Info("alert rules loaded", "rules", len(rules), "from", rulesFrom)

	collectors.SetWatchServices(cfg.WatchServices)
	cmdline, err := collectors.ParseCmdlineMode(cfg.ProcessCmdline)
	if err != nil { // config.Load already checked this
		fmt.Fprintln(stderr, "pisens:", err)
		return 2
	}
	collectors.SetProcessCmdline(cmdline)
	collectors.SetNetSkip(cfg.NetSkip)

	var serverOpts []server.Option
	if cfg.AuthEnabled() {
		basic, err := auth.NewBasic(cfg.AuthUser, cfg.AuthPasswordHash, logger)
		if err != nil { // config.Load already checked this
			fmt.Fprintln(stderr, "pisens:", err)
			return 2
		}
		serverOpts = append(serverOpts, server.WithBasicAuth(basic))
		logger.Info("basic auth enabled", "user", cfg.AuthUser)
	} else {
		logger.Info("basic auth disabled; the dashboard is open to anyone who can reach it")
	}

	store, err := history.Open(ctx, cfg.DataDir)
	if err != nil {
		logger.Error("open history database", "err", err)
		return 1
	}
	logger.Info("history database opened", "path", store.Path())
	retention := history.Retention{
		Raw:     cfg.HistoryRawRetention,
		Minute:  cfg.History1mRetention,
		Quarter: cfg.History15mRetention,
	}
	store.SetRetention(retention)

	// The energy meter is fed by the power collector. A broken state file is
	// moved aside and the totals start from zero rather than stopping
	// pisens over them.
	meter, err := energy.Open(cfg.DataDir, energy.Options{
		MaxGap: max(energy.DefaultMaxGap, 3*cfg.Interval),
		Logger: logger,
	})
	if err != nil {
		logger.Warn("energy totals", "err", err)
	}
	collectors.SetEnergy(meter, cfg.EnergyPrice, cfg.EnergyCurrency)
	logger.Info("energy meter", "path", meter.Path(), "price", cfg.EnergyPrice, "currency", cfg.EnergyCurrency)

	sampler := metrics.NewSampler(metrics.Options{Interval: cfg.Interval, Logger: logger})

	// Subscribe before the sampler starts so the first snapshot is stored too.
	// The subscription closes when the sampler stops, which ends the writer
	// after it has written what is still pending.
	snaps, unsubscribe := sampler.Subscribe()
	defer unsubscribe()
	writer := history.NewWriter(store, cfg.HistoryInterval, logger)
	writerDone := make(chan error, 1)
	go func() { writerDone <- writer.Run(ctx, snaps) }()

	// A sample reaches the database within about a history interval plus a
	// sample interval of its timestamp; the second sample interval is slack.
	downsampler := history.NewDownsampler(store, history.DownsamplerOptions{
		Retention: retention,
		Delay:     cfg.HistoryInterval + 2*cfg.Interval,
		Logger:    logger,
	})
	downsamplerDone := make(chan struct{})
	go func() {
		defer close(downsamplerDone)
		downsampler.Run(ctx)
	}()

	// The evaluator gets its own subscription, also taken before the sampler
	// starts so the first sample is evaluated.
	evaluator := alerts.NewEvaluator(rules, logger)
	alertSnaps, unsubscribeAlerts := sampler.Subscribe()
	defer unsubscribeAlerts()

	// Notifiers only subscribe, and only start a goroutine, when one is
	// configured. They subscribe before the evaluator starts so the first
	// transition is not missed.
	dispatcher := newDispatcher(cfg, logger)
	logger.Info("alert notifications", "notifiers", dispatcher.Describe(), "cooldown", cfg.NotifyCooldown.String())
	dispatcherDone := make(chan struct{})
	if dispatcher.Enabled() {
		transitions, unsubscribeTransitions := evaluator.SubscribeTransitions()
		defer unsubscribeTransitions()
		go func() {
			defer close(dispatcherDone)
			dispatcher.Run(ctx, transitions)
		}()
	} else {
		close(dispatcherDone)
	}
	evaluatorDone := make(chan struct{})
	go func() {
		evaluator.Run(ctx, alertSnaps)
		close(evaluatorDone)
	}()

	samplerDone := make(chan error, 1)
	go func() { samplerDone <- sampler.Run(ctx) }()

	srv := server.New(cfg.Listen, version, web.Dist(), logger, sampler, evaluator, store, serverOpts...)
	if cfg.Prometheus {
		srv.EnablePrometheus()
	}
	logger.Info("prometheus metrics", "enabled", cfg.Prometheus, "path", "/metrics")
	err = srv.Run(ctx)
	stop() // stop the sampler too if the server failed on its own
	if serr := <-samplerDone; serr != nil {
		logger.Error("sampler failed", "err", serr)
	}
	// The sampler has stopped, so no reading arrives after this.
	if merr := meter.Close(); merr != nil {
		logger.Error("save energy totals", "err", merr)
	}
	<-evaluatorDone
	<-dispatcherDone
	if werr := <-writerDone; werr != nil {
		logger.Error("history writer failed to save pending samples", "err", werr)
	}
	<-downsamplerDone
	if cerr := store.Close(); cerr != nil {
		logger.Error("close history database", "err", cerr)
	}
	if err != nil {
		logger.Error("server failed", "err", err)
		return 1
	}

	logger.Info("pisens stopped")
	return 0
}

// newDispatcher creates the alert notifiers configured in cfg.
func newDispatcher(cfg config.Config, logger *slog.Logger) *notify.Dispatcher {
	var notifiers []notify.Notifier
	if cfg.NotifyWebhook != "" {
		notifiers = append(notifiers, notify.NewWebhook(cfg.NotifyWebhook))
	}
	if cfg.NotifyNtfy != "" {
		notifiers = append(notifiers, notify.NewNtfy(cfg.NotifyNtfy))
	}
	hostname, err := os.Hostname()
	if err != nil && len(notifiers) > 0 {
		logger.Warn("hostname unknown; notifications leave it empty", "err", err)
	}
	return notify.New(notifiers, notify.Options{Cooldown: cfg.NotifyCooldown, Hostname: hostname, Logger: logger})
}
