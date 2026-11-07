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
	"syscall"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
	_ "github.com/Berthelmaster/pi-sens/internal/collectors" // registers the collectors
	"github.com/Berthelmaster/pi-sens/internal/config"
	"github.com/Berthelmaster/pi-sens/internal/history"
	"github.com/Berthelmaster/pi-sens/internal/metrics"
	"github.com/Berthelmaster/pi-sens/internal/server"
	"github.com/Berthelmaster/pi-sens/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
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
	)
	rulesFrom := "built-in"
	if cfg.AlertsFile != "" {
		rulesFrom = cfg.AlertsFile
	}
	logger.Info("alert rules loaded", "rules", len(rules), "from", rulesFrom)

	store, err := history.Open(ctx, cfg.DataDir)
	if err != nil {
		logger.Error("open history database", "err", err)
		return 1
	}
	logger.Info("history database opened", "path", store.Path())

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
		Retention: history.Retention{
			Raw:     cfg.HistoryRawRetention,
			Minute:  cfg.History1mRetention,
			Quarter: cfg.History15mRetention,
		},
		Delay:  cfg.HistoryInterval + 2*cfg.Interval,
		Logger: logger,
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
	evaluatorDone := make(chan struct{})
	go func() {
		evaluator.Run(ctx, alertSnaps)
		close(evaluatorDone)
	}()

	samplerDone := make(chan error, 1)
	go func() { samplerDone <- sampler.Run(ctx) }()

	srv := server.New(cfg.Listen, version, web.Dist(), logger, sampler, evaluator, store)
	err = srv.Run(ctx)
	stop() // stop the sampler too if the server failed on its own
	if serr := <-samplerDone; serr != nil {
		logger.Error("sampler failed", "err", serr)
	}
	<-evaluatorDone
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
