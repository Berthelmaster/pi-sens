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
	"strconv"
	"strings"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/auth"
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

	DefaultProcessCmdline = ProcessCmdlineRedacted

	// DefaultNetSkip leaves out loopback and the veth pairs and bridges
	// Docker creates.
	DefaultNetSkip = "lo,veth*,docker*,br-*"
)

// ProcessCmdline values: how much of the process command lines the
// processes collector reports.
const (
	// ProcessCmdlineOff reports only the process name.
	ProcessCmdlineOff = "off"
	// ProcessCmdlineRedacted reports the command line with values that look
	// like secrets replaced.
	ProcessCmdlineRedacted = "redacted"
	// ProcessCmdlineFull reports the command line as it is.
	ProcessCmdlineFull = "full"
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
	EnvWatchServices  = "PISENS_WATCH_SERVICES"
	EnvProcessCmdline = "PISENS_PROCESS_CMDLINE"
	EnvNetSkip        = "PISENS_NET_SKIP"

	EnvPrometheus       = "PISENS_PROMETHEUS"
	EnvAuthUser         = "PISENS_AUTH_USER"
	EnvAuthPasswordHash = "PISENS_AUTH_PASSWORD_HASH"
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
	// WatchServices are the systemd units whose state is reported on every
	// sample, e.g. ["ssh", "pisens.service"]. Empty means none.
	WatchServices []string
	// ProcessCmdline is how much of each top process's command line the
	// snapshot shows: ProcessCmdlineOff, ProcessCmdlineRedacted or
	// ProcessCmdlineFull.
	ProcessCmdline string
	// NetSkip are the network interfaces the network collector leaves out:
	// names, or name prefixes when they end in "*". Empty means none.
	NetSkip []string
	// Prometheus turns on GET /metrics in the Prometheus text format.
	Prometheus bool
	// AuthUser and AuthPasswordHash turn on HTTP basic auth for every route
	// except /api/health. AuthPasswordHash is a bcrypt hash. Either both
	// are set or neither; neither leaves the server open.
	AuthUser         string
	AuthPasswordHash string
}

// AuthEnabled reports whether basic auth is configured.
func (c Config) AuthEnabled() bool {
	return c.AuthUser != "" || c.AuthPasswordHash != ""
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

		ProcessCmdline: DefaultProcessCmdline,

		Prometheus: true,
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
	if v := getenv(EnvProcessCmdline); v != "" {
		cfg.ProcessCmdline = v
	}
	if v := getenv(EnvPrometheus); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			envErrs = append(envErrs, fmt.Errorf("invalid %s %q: want true or false", EnvPrometheus, v))
		} else {
			cfg.Prometheus = parsed
		}
	}

	cfg.AuthUser = getenv(EnvAuthUser)
	cfg.AuthPasswordHash = getenv(EnvAuthPasswordHash)

	watch := getenv(EnvWatchServices)
	netSkip := DefaultNetSkip
	if v := getenv(EnvNetSkip); v != "" {
		netSkip = v
	}

	// The environment values become the flag defaults, so an explicit flag wins.
	fs := flag.NewFlagSet("pisens", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: pisens [flags]\n"+
			"       pisens hash-password    read a password from stdin and print its bcrypt hash\n\nFlags:\n")
		fs.PrintDefaults()
	}
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
	fs.StringVar(&watch, "watch-services", watch,
		"comma separated systemd services to report the state of, e.g. ssh,pisens (env "+EnvWatchServices+")")
	fs.StringVar(&cfg.ProcessCmdline, "process-cmdline", cfg.ProcessCmdline,
		"how much of the top processes' command lines to show: off (name only), redacted (secrets hidden) or full (env "+EnvProcessCmdline+")")
	fs.StringVar(&netSkip, "net-skip", netSkip,
		"comma separated network interfaces to leave out, names or prefixes ending in *; --net-skip= reports all (env "+EnvNetSkip+")")
	fs.BoolVar(&cfg.Prometheus, "prometheus", cfg.Prometheus,
		"serve metrics for Prometheus at /metrics; --prometheus=false turns it off (env "+EnvPrometheus+")")
	fs.StringVar(&cfg.AuthUser, "auth-user", cfg.AuthUser,
		"user name for HTTP basic auth; needs --auth-password-hash (env "+EnvAuthUser+")")
	fs.StringVar(&cfg.AuthPasswordHash, "auth-password-hash", cfg.AuthPasswordHash,
		"bcrypt hash of the basic auth password, from pisens hash-password; needs --auth-user (env "+EnvAuthPasswordHash+")")
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
	cfg.NetSkip = splitList(netSkip)
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
	switch {
	case c.AuthUser != "" && c.AuthPasswordHash == "":
		errs = append(errs, errors.New("auth user is set without an auth password hash; set both or neither"))
	case c.AuthUser == "" && c.AuthPasswordHash != "":
		errs = append(errs, errors.New("auth password hash is set without an auth user; set both or neither"))
	case c.AuthEnabled():
		if err := auth.Validate(c.AuthUser, c.AuthPasswordHash); err != nil {
			errs = append(errs, err)
		}
	}
	switch c.ProcessCmdline {
	case ProcessCmdlineOff, ProcessCmdlineRedacted, ProcessCmdlineFull:
	default:
		errs = append(errs, fmt.Errorf("process command line must be off, redacted or full, got %q", c.ProcessCmdline))
	}
	for _, name := range c.WatchServices {
		if strings.ContainsAny(name, " \t/") {
			errs = append(errs, fmt.Errorf("invalid service name %q in the watch list", name))
		}
	}
	for _, p := range c.NetSkip {
		if strings.ContainsAny(p, " \t/:") || strings.Contains(strings.TrimSuffix(p, "*"), "*") {
			errs = append(errs, fmt.Errorf("invalid network interface %q in the skip list: want a name, or a prefix ending in *", p))
		}
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
