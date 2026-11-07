// Package config holds the runtime configuration for pisens.
//
// Every setting can be given as a command-line flag or as a PISENS_* environment
// variable. Flags take precedence over environment variables, which take
// precedence over the built-in defaults. New settings belong in Config and in
// Load so they get the same behaviour.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// Defaults used when neither a flag nor an environment variable is set.
const (
	DefaultListen   = ":8080"
	DefaultInterval = 2 * time.Second
	DefaultDataDir  = "./data"

	DefaultHistoryInterval = 10 * time.Second
)

// Environment variable names.
const (
	EnvListen   = "PISENS_LISTEN"
	EnvInterval = "PISENS_INTERVAL"
	EnvDataDir  = "PISENS_DATA_DIR"

	EnvHistoryInterval = "PISENS_HISTORY_INTERVAL"
	EnvWatchServices   = "PISENS_WATCH_SERVICES"
)

// Config is the resolved runtime configuration.
type Config struct {
	// Listen is the address the HTTP server binds to, e.g. ":8080".
	Listen string
	// Interval is how often collectors are sampled.
	Interval time.Duration
	// DataDir is where pisens keeps persistent data.
	DataDir string
	// HistoryInterval is how often samples are written to the history
	// database.
	HistoryInterval time.Duration
	// WatchServices are the systemd units whose state is reported on every
	// sample, e.g. ["ssh", "pisens.service"]. Empty means none.
	WatchServices []string
}

// ErrHelp is returned when the user asked for usage with -h or --help.
var ErrHelp = flag.ErrHelp

// ErrVersion is returned when the user asked for the version with --version.
// The caller prints it and exits.
var ErrVersion = errors.New("version requested")

// Load resolves the configuration from args (without the program name) and
// the environment, looked up through getenv. Usage and parse errors are
// written to output.
func Load(args []string, getenv func(string) string, output io.Writer) (Config, error) {
	cfg := Config{
		Listen:   DefaultListen,
		Interval: DefaultInterval,
		DataDir:  DefaultDataDir,

		HistoryInterval: DefaultHistoryInterval,
	}

	// Invalid environment values are reported after the flags are parsed, so
	// --help and --version still work with a broken environment file.
	var envErrs []error
	if v := getenv(EnvListen); v != "" {
		cfg.Listen = v
	}
	if v := getenv(EnvInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			envErrs = append(envErrs, fmt.Errorf("invalid %s %q: %w", EnvInterval, v, err))
		} else {
			cfg.Interval = d
		}
	}
	if v := getenv(EnvDataDir); v != "" {
		cfg.DataDir = v
	}
	if v := getenv(EnvHistoryInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			envErrs = append(envErrs, fmt.Errorf("invalid %s %q: %w", EnvHistoryInterval, v, err))
		} else {
			cfg.HistoryInterval = d
		}
	}

	watch := getenv(EnvWatchServices)

	// The environment values become the flag defaults, so an explicit flag wins.
	fs := flag.NewFlagSet("pisens", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "HTTP listen address (env "+EnvListen+")")
	fs.DurationVar(&cfg.Interval, "interval", cfg.Interval, "sample interval (env "+EnvInterval+")")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory (env "+EnvDataDir+")")
	fs.DurationVar(&cfg.HistoryInterval, "history-interval", cfg.HistoryInterval,
		"how often samples are written to the history database (env "+EnvHistoryInterval+")")
	fs.StringVar(&watch, "watch-services", watch,
		"comma separated systemd services to report the state of, e.g. ssh,pisens (env "+EnvWatchServices+")")
	showVersion := fs.Bool("version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if *showVersion {
		return Config{}, ErrVersion
	}
	if err := errors.Join(envErrs...); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	cfg.WatchServices = splitList(watch)
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// splitList splits a comma separated list, trimming spaces and dropping empty
// items. It returns nil for an empty list.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (c Config) validate() error {
	var errs []error
	if c.Listen == "" {
		errs = append(errs, errors.New("listen address must not be empty"))
	}
	if c.Interval <= 0 {
		errs = append(errs, fmt.Errorf("interval must be positive, got %s", c.Interval))
	}
	if c.HistoryInterval <= 0 {
		errs = append(errs, fmt.Errorf("history interval must be positive, got %s", c.HistoryInterval))
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("data directory must not be empty"))
	}
	for _, name := range c.WatchServices {
		if strings.ContainsAny(name, " \t/") {
			errs = append(errs, fmt.Errorf("invalid service name %q in the watch list", name))
		}
	}
	return errors.Join(errs...)
}
