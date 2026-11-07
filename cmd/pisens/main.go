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

	"github.com/Berthelmaster/pi-sens/internal/config"
	"github.com/Berthelmaster/pi-sens/internal/server"
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
	)

	srv := server.New(cfg.Listen, version, logger)
	if err := srv.Run(ctx); err != nil {
		logger.Error("server failed", "err", err)
		return 1
	}

	logger.Info("pisens stopped")
	return 0
}
