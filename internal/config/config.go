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
	"net/url"
	"time"
)

// Defaults used when neither a flag nor an environment variable is set.
const (
	DefaultListen   = ":8080"
	DefaultInterval = 2 * time.Second
	DefaultDataDir  = "./data"

	DefaultHistoryInterval = 10 * time.Second

	DefaultHistoryRawRetention = 24 * time.Hour
	DefaultHistory1mRetention  = 7 * 24 * time.Hour
	DefaultHistory15mRetention = 90 * 24 * time.Hour

	DefaultNotifyCooldown = 15 * time.Minute
)

// Environment variable names.
const (
	EnvListen   = "PISENS_LISTEN"
	EnvInterval = "PISENS_INTERVAL"
	EnvDataDir  = "PISENS_DATA_DIR"

	EnvHistoryInterval = "PISENS_HISTORY_INTERVAL"

	EnvHistoryRawRetention = "PISENS_HISTORY_RAW_RETENTION"
	EnvHistory1mRetention  = "PISENS_HISTORY_1M_RETENTION"
	EnvHistory15mRetention = "PISENS_HISTORY_15M_RETENTION"

	EnvAlerts = "PISENS_ALERTS_FILE"

	EnvNotifyWebhook  = "PISENS_NOTIFY_WEBHOOK"
	EnvNotifyNtfy     = "PISENS_NOTIFY_NTFY"
	EnvNotifyCooldown = "PISENS_NOTIFY_COOLDOWN"
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
	// HistoryRawRetention, History1mRetention and History15mRetention are
	// how long the history database keeps raw samples and the 1 minute and
	// 15 minute rollups.
	HistoryRawRetention time.Duration
	History1mRetention  time.Duration
	History15mRetention time.Duration
	// AlertsFile is a YAML file of alert rules that replaces the built-in
	// ones. Empty means use the built-in rules.
	AlertsFile string
	// NotifyWebhook is a URL that gets alert notifications as JSON. Empty
	// means no webhook.
	NotifyWebhook string
	// NotifyNtfy is an ntfy topic URL that gets alert notifications. Empty
	// means no ntfy.
	NotifyNtfy string
	// NotifyCooldown is the shortest time between two firing notifications
	// for the same alert. Zero turns the cooldown off.
	NotifyCooldown time.Duration
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

		HistoryRawRetention: DefaultHistoryRawRetention,
		History1mRetention:  DefaultHistory1mRetention,
		History15mRetention: DefaultHistory15mRetention,

		NotifyCooldown: DefaultNotifyCooldown,
	}

	// Invalid environment values are reported after the flags are parsed, so
	// --help and --version still work with a broken environment file.
	var envErrs []error
	if v := getenv(EnvListen); v != "" {
		cfg.Listen = v
	}
	if v := getenv(EnvDataDir); v != "" {
		cfg.DataDir = v
	}
	durations := []struct {
		env string
		dst *time.Duration
	}{
		{EnvInterval, &cfg.Interval},
		{EnvHistoryInterval, &cfg.HistoryInterval},
		{EnvHistoryRawRetention, &cfg.HistoryRawRetention},
		{EnvHistory1mRetention, &cfg.History1mRetention},
		{EnvHistory15mRetention, &cfg.History15mRetention},
		{EnvNotifyCooldown, &cfg.NotifyCooldown},
	}
	for _, d := range durations {
		v := getenv(d.env)
		if v == "" {
			continue
		}
		parsed, err := time.ParseDuration(v)
		if err != nil {
			envErrs = append(envErrs, fmt.Errorf("invalid %s %q: %w", d.env, v, err))
			continue
		}
		*d.dst = parsed
	}
	cfg.AlertsFile = getenv(EnvAlerts)
	cfg.NotifyWebhook = getenv(EnvNotifyWebhook)
	cfg.NotifyNtfy = getenv(EnvNotifyNtfy)

	// The environment values become the flag defaults, so an explicit flag wins.
	fs := flag.NewFlagSet("pisens", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "HTTP listen address (env "+EnvListen+")")
	fs.DurationVar(&cfg.Interval, "interval", cfg.Interval, "sample interval (env "+EnvInterval+")")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory (env "+EnvDataDir+")")
	fs.DurationVar(&cfg.HistoryInterval, "history-interval", cfg.HistoryInterval,
		"how often samples are written to the history database (env "+EnvHistoryInterval+")")
	fs.DurationVar(&cfg.HistoryRawRetention, "history-raw-retention", cfg.HistoryRawRetention,
		"how long raw history samples are kept (env "+EnvHistoryRawRetention+")")
	fs.DurationVar(&cfg.History1mRetention, "history-1m-retention", cfg.History1mRetention,
		"how long 1 minute history averages are kept (env "+EnvHistory1mRetention+")")
	fs.DurationVar(&cfg.History15mRetention, "history-15m-retention", cfg.History15mRetention,
		"how long 15 minute history averages are kept (env "+EnvHistory15mRetention+")")
	fs.StringVar(&cfg.AlertsFile, "alerts-file", cfg.AlertsFile, "YAML file of alert rules that replaces the built-in rules (env "+EnvAlerts+")")
	fs.StringVar(&cfg.NotifyWebhook, "notify-webhook", cfg.NotifyWebhook,
		"URL to POST alert notifications to as JSON (env "+EnvNotifyWebhook+")")
	fs.StringVar(&cfg.NotifyNtfy, "notify-ntfy", cfg.NotifyNtfy,
		"ntfy topic URL for alert notifications, e.g. https://ntfy.sh/my-pi (env "+EnvNotifyNtfy+")")
	fs.DurationVar(&cfg.NotifyCooldown, "notify-cooldown", cfg.NotifyCooldown,
		"shortest time between two notifications that the same alert fires, 0 for none (env "+EnvNotifyCooldown+")")
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
	if c.HistoryInterval <= 0 {
		errs = append(errs, fmt.Errorf("history interval must be positive, got %s", c.HistoryInterval))
	}
	retentions := []struct {
		name string
		d    time.Duration
	}{
		{"history raw retention", c.HistoryRawRetention},
		{"history 1m retention", c.History1mRetention},
		{"history 15m retention", c.History15mRetention},
	}
	for _, r := range retentions {
		if r.d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive, got %s", r.name, r.d))
		}
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("data directory must not be empty"))
	}
	if c.NotifyCooldown < 0 {
		errs = append(errs, fmt.Errorf("notify cooldown must not be negative, got %s", c.NotifyCooldown))
	}
	if err := checkURL(c.NotifyWebhook); err != nil {
		errs = append(errs, fmt.Errorf("notify webhook %w", err))
	}
	if err := checkURL(c.NotifyNtfy); err != nil {
		errs = append(errs, fmt.Errorf("notify ntfy %w", err))
	}
	return errors.Join(errs...)
}

// checkURL checks that a notification URL, if set, is an absolute http or
// https URL. The error leaves the URL out because it may hold a secret.
func checkURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an http:// or https:// URL")
	}
	return nil
}
