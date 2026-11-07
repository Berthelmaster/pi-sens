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
	)
	rulesFrom := "built-in"
	if cfg.AlertsFile != "" {
		rulesFrom = cfg.AlertsFile
	}
	logger.Info("alert rules loaded", "rules", len(rules), "from", rulesFrom)

	sampler := metrics.NewSampler(metrics.Options{Interval: cfg.Interval, Logger: logger})
	evaluator := alerts.NewEvaluator(rules, logger)
	// Subscribe before the sampler starts so the first sample is evaluated.
	snaps, _ := sampler.Subscribe() // closed when the sampler stops
	evaluatorDone := make(chan struct{})
	go func() {
		evaluator.Run(ctx, snaps)
		close(evaluatorDone)
	}()
	samplerDone := make(chan error, 1)
	go func() { samplerDone <- sampler.Run(ctx) }()

	srv := server.New(cfg.Listen, version, web.Dist(), logger, sampler, evaluator)
	err = srv.Run(ctx)
	stop() // stop the sampler too if the server failed on its own
	if serr := <-samplerDone; serr != nil {
		logger.Error("sampler failed", "err", serr)
	}
	<-evaluatorDone
	if err != nil {
		logger.Error("server failed", "err", err)
		return 1
	}

	logger.Info("pisens stopped")
	return 0
}
