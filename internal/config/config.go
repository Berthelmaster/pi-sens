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
	"time"
)

// Defaults used when neither a flag nor an environment variable is set.
const (
	DefaultListen   = ":8080"
	DefaultInterval = 2 * time.Second
	DefaultDataDir  = "./data"
)

// Environment variable names.
const (
	EnvListen   = "PISENS_LISTEN"
	EnvInterval = "PISENS_INTERVAL"
	EnvDataDir  = "PISENS_DATA_DIR"
	EnvAlerts   = "PISENS_ALERTS_FILE"
)

// Config is the resolved runtime configuration.
type Config struct {
	// Listen is the address the HTTP server binds to, e.g. ":8080".
	Listen string
	// Interval is how often collectors are sampled.
	Interval time.Duration
	// DataDir is where pisens keeps persistent data.
	DataDir string
	// AlertsFile is a YAML file of alert rules that replaces the built-in
	// ones. Empty means use the built-in rules.
	AlertsFile string
}

// ErrHelp is returned when the user asked for usage with -h or --help.
var ErrHelp = flag.ErrHelp

// Load resolves the configuration from args (without the program name) and
// the environment, looked up through getenv. Usage and parse errors are
// written to output.
func Load(args []string, getenv func(string) string, output io.Writer) (Config, error) {
	cfg := Config{
		Listen:   DefaultListen,
		Interval: DefaultInterval,
		DataDir:  DefaultDataDir,
	}

	if v := getenv(EnvListen); v != "" {
		cfg.Listen = v
	}
	if v := getenv(EnvInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("invalid %s %q: %w", EnvInterval, v, err)
		}
		cfg.Interval = d
	}
	if v := getenv(EnvDataDir); v != "" {
		cfg.DataDir = v
	}
	cfg.AlertsFile = getenv(EnvAlerts)

	// The environment values become the flag defaults, so an explicit flag wins.
	fs := flag.NewFlagSet("pisens", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "HTTP listen address (env "+EnvListen+")")
	fs.DurationVar(&cfg.Interval, "interval", cfg.Interval, "sample interval (env "+EnvInterval+")")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory (env "+EnvDataDir+")")
	fs.StringVar(&cfg.AlertsFile, "alerts-file", cfg.AlertsFile, "YAML file of alert rules that replaces the built-in rules (env "+EnvAlerts+")")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error
	if c.Listen == "" {
		errs = append(errs, errors.New("listen address must not be empty"))
	}
	if c.Interval <= 0 {
		errs = append(errs, fmt.Errorf("interval must be positive, got %s", c.Interval))
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("data directory must not be empty"))
	}
	return errors.Join(errs...)
}
