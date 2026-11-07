// Command pisens serves Raspberry Pi system metrics over HTTP.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	os.Exit(run())
}

func run() int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(os.Args[1:], os.Getenv, os.Stderr)
	if errors.Is(err, config.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pisens:", err)
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
	)

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

	samplerDone := make(chan error, 1)
	go func() { samplerDone <- sampler.Run(ctx) }()

	srv := server.New(cfg.Listen, version, web.Dist(), logger, sampler)
	err = srv.Run(ctx)
	stop() // stop the sampler too if the server failed on its own
	if serr := <-samplerDone; serr != nil {
		logger.Error("sampler failed", "err", serr)
	}
	if werr := <-writerDone; werr != nil {
		logger.Error("history writer failed to save pending samples", "err", werr)
	}
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
