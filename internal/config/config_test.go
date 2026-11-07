package config

import (
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

// testHash is a bcrypt hash of "pw".
const testHash = "$2a$10$azHQ.jqYzZIyXkRa.ETdgeauf8sdkH.P0fxNsJ6DnqpKRaQXd7dzG"

var defaultNetSkip = []string{"lo", "veth*", "docker*", "br-*"}

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want Config
	}{
		{
			name: "defaults",
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: 24 * time.Hour, History1mRetention: 168 * time.Hour, History15mRetention: 2160 * time.Hour,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: 15 * time.Minute,
				Prometheus:     true,
			},
		},
		{
			name: "env only",
			env: map[string]string{
				EnvListen:   "127.0.0.1:9000",
				EnvInterval: "500ms",
				EnvDataDir:  "/var/lib/pisens",

				EnvHistoryInterval: "30s",

				EnvHistoryRawRetention: "12h",
				EnvHistory1mRetention:  "72h",
				EnvHistory15mRetention: "720h",

				EnvAlerts: "/etc/pisens/alerts.yaml",

				EnvNotifyWebhook:  "https://hooks.example.com/pisens?token=abc",
				EnvNotifyNtfy:     "https://ntfy.sh/my-pi",
				EnvNotifyCooldown: "5m",
			},
			want: Config{
				Listen: "127.0.0.1:9000", Interval: 500 * time.Millisecond, DataDir: "/var/lib/pisens", HistoryInterval: 30 * time.Second,
				HistoryRawRetention: 12 * time.Hour, History1mRetention: 72 * time.Hour, History15mRetention: 720 * time.Hour,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				AlertsFile:     "/etc/pisens/alerts.yaml",
				NotifyWebhook:  "https://hooks.example.com/pisens?token=abc", NotifyNtfy: "https://ntfy.sh/my-pi",
				NotifyCooldown: 5 * time.Minute,
				Prometheus:     true,
			},
		},
		{
			name: "flags only",
			args: []string{
				"--listen", ":9090", "--interval=5s", "-data-dir", "/tmp/pisens", "--history-interval", "1m",
				"--history-raw-retention", "6h", "--history-1m-retention=48h", "--history-15m-retention", "8760h",
				"--alerts-file", "rules.yaml",
				"--notify-webhook", "http://192.168.1.10:9000/hook", "--notify-ntfy=https://ntfy.example.com/pi",
				"--notify-cooldown", "0",
			},
			want: Config{
				Listen: ":9090", Interval: 5 * time.Second, DataDir: "/tmp/pisens", HistoryInterval: time.Minute,
				HistoryRawRetention: 6 * time.Hour, History1mRetention: 48 * time.Hour, History15mRetention: 8760 * time.Hour,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				AlertsFile:     "rules.yaml",
				NotifyWebhook:  "http://192.168.1.10:9000/hook", NotifyNtfy: "https://ntfy.example.com/pi",
				Prometheus: true,
			},
		},
		{
			name: "flags win over env",
			args: []string{
				"--listen", ":9090", "--interval", "1m", "--history-interval", "20s", "--history-raw-retention", "2h",
				"--alerts-file", "flag.yaml", "--notify-ntfy", "https://ntfy.sh/flag", "--notify-cooldown", "1h",
			},
			env: map[string]string{
				EnvListen:   ":7070",
				EnvInterval: "10s",
				EnvDataDir:  "/from/env",

				EnvHistoryInterval: "5m",

				EnvHistoryRawRetention: "1h",
				EnvHistory1mRetention:  "24h",

				EnvAlerts: "env.yaml",

				EnvNotifyWebhook:  "https://env.example.com/hook",
				EnvNotifyNtfy:     "https://ntfy.sh/env",
				EnvNotifyCooldown: "2m",
			},
			want: Config{
				Listen: ":9090", Interval: time.Minute, DataDir: "/from/env", HistoryInterval: 20 * time.Second,
				HistoryRawRetention: 2 * time.Hour, History1mRetention: 24 * time.Hour, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				AlertsFile:     "flag.yaml",
				NotifyWebhook:  "https://env.example.com/hook", NotifyNtfy: "https://ntfy.sh/flag",
				NotifyCooldown: time.Hour,
				Prometheus:     true,
			},
		},
		{
			name: "watch services from env",
			env:  map[string]string{EnvWatchServices: " ssh, pisens.service,,nginx "},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				WatchServices:  []string{"ssh", "pisens.service", "nginx"},
				Prometheus:     true,
			},
		},
		{
			name: "watch services flag wins over env",
			args: []string{"--watch-services", "cron"},
			env:  map[string]string{EnvWatchServices: "ssh,nginx"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				WatchServices:  []string{"cron"},
				Prometheus:     true,
			},
		},
		{
			name: "auth from env",
			env:  map[string]string{EnvAuthUser: "admin", EnvAuthPasswordHash: testHash},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
				AuthUser:       "admin", AuthPasswordHash: testHash,
			},
		},
		{
			name: "auth flags win over env",
			args: []string{"--auth-user", "pi", "--auth-password-hash", testHash},
			env:  map[string]string{EnvAuthUser: "admin", EnvAuthPasswordHash: "$2a$10$fromtheenvironment"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
				AuthUser:       "pi", AuthPasswordHash: testHash,
			},
		},
		{
			name: "empty watch services flag clears env",
			args: []string{"--watch-services="},
			env:  map[string]string{EnvWatchServices: "ssh"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
			},
		},
		{
			name: "process cmdline from env",
			env:  map[string]string{EnvProcessCmdline: "off"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: ProcessCmdlineOff,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
			},
		},
		{
			name: "process cmdline flag wins over env",
			args: []string{"--process-cmdline", "full"},
			env:  map[string]string{EnvProcessCmdline: "off"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: ProcessCmdlineFull,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
			},
		},
		{
			name: "net skip from env",
			env:  map[string]string{EnvNetSkip: " lo, veth*,,cni*, virbr*,tailscale0 "},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        []string{"lo", "veth*", "cni*", "virbr*", "tailscale0"},
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
			},
		},
		{
			name: "net skip flag wins over env",
			args: []string{"--net-skip", "lo,podman*"},
			env:  map[string]string{EnvNetSkip: "lo,cni*"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        []string{"lo", "podman*"},
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
			},
		},
		{
			name: "empty net skip flag reports every interface",
			args: []string{"--net-skip="},
			env:  map[string]string{EnvNetSkip: "lo"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
			},
		},
		{
			name: "energy price from env",
			env:  map[string]string{EnvEnergyPrice: "2.45", EnvEnergyCurrency: "DKK"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				EnergyPrice:    2.45, EnergyCurrency: "DKK",
				Prometheus: true,
			},
		},
		{
			name: "energy price flag wins over env",
			args: []string{"--energy-price", "0.3", "--energy-currency=EUR"},
			env:  map[string]string{EnvEnergyPrice: "2.45", EnvEnergyCurrency: "DKK"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				EnergyPrice:    0.3, EnergyCurrency: "EUR",
				Prometheus: true,
			},
		},
		{
			name: "prometheus off by flag",
			args: []string{"--prometheus=false"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
			},
		},
		{
			name: "prometheus off by env",
			env:  map[string]string{EnvPrometheus: "false"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
			},
		},
		{
			name: "prometheus flag wins over env",
			args: []string{"--prometheus"},
			env:  map[string]string{EnvPrometheus: "0"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				HistoryRawRetention: DefaultHistoryRawRetention, History1mRetention: DefaultHistory1mRetention, History15mRetention: DefaultHistory15mRetention,
				NetSkip:        defaultNetSkip,
				ProcessCmdline: DefaultProcessCmdline,
				NotifyCooldown: DefaultNotifyCooldown,
				Prometheus:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(tt.args, envFrom(tt.env), io.Discard)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "bad env interval", env: map[string]string{EnvInterval: "soon"}},
		{name: "bad flag interval", args: []string{"--interval", "soon"}},
		{name: "zero interval", args: []string{"--interval", "0s"}},
		{name: "negative interval", env: map[string]string{EnvInterval: "-1s"}},
		{name: "bad env history interval", env: map[string]string{EnvHistoryInterval: "often"}},
		{name: "zero history interval", args: []string{"--history-interval", "0s"}},
		{name: "negative history interval", env: map[string]string{EnvHistoryInterval: "-10s"}},
		{name: "bad env raw retention", env: map[string]string{EnvHistoryRawRetention: "1 day"}},
		{name: "bad env 1m retention", env: map[string]string{EnvHistory1mRetention: "7d"}},
		{name: "bad env 15m retention", env: map[string]string{EnvHistory15mRetention: "forever"}},
		{name: "zero raw retention", args: []string{"--history-raw-retention", "0s"}},
		{name: "negative 1m retention", env: map[string]string{EnvHistory1mRetention: "-1h"}},
		{name: "zero 15m retention", args: []string{"--history-15m-retention=0"}},
		{name: "empty listen flag", args: []string{"--listen", ""}},
		{name: "empty data dir flag", args: []string{"--data-dir="}},
		{name: "bad env notify cooldown", env: map[string]string{EnvNotifyCooldown: "15 minutes"}},
		{name: "negative notify cooldown", args: []string{"--notify-cooldown", "-1m"}},
		{name: "webhook without scheme", args: []string{"--notify-webhook", "hooks.example.com/pisens"}},
		{name: "webhook with other scheme", env: map[string]string{EnvNotifyWebhook: "ftp://example.com/hook"}},
		{name: "ntfy topic without host", args: []string{"--notify-ntfy", "https:///topic"}},
		{name: "ntfy bare topic", env: map[string]string{EnvNotifyNtfy: "my-pi-alerts"}},
		{name: "unknown flag", args: []string{"--nope"}},
		{name: "space in watched service", args: []string{"--watch-services", "ssh,my service"}},
		{name: "path as watched service", env: map[string]string{EnvWatchServices: "/etc/systemd/ssh.service"}},
		{name: "bad env process cmdline", env: map[string]string{EnvProcessCmdline: "partial"}},
		{name: "bad flag process cmdline", args: []string{"--process-cmdline", "Full"}},
		{name: "space in net skip", args: []string{"--net-skip", "lo,veth *"}},
		{name: "star inside net skip", env: map[string]string{EnvNetSkip: "lo,v*th"}},
		{name: "path as net skip", env: map[string]string{EnvNetSkip: "/sys/class/net/lo"}},
		{name: "bad env energy price", env: map[string]string{EnvEnergyPrice: "2,45"}},
		{name: "negative energy price", args: []string{"--energy-price", "-1"}},
		{name: "NaN energy price", env: map[string]string{EnvEnergyPrice: "NaN"}},
		{name: "bad env prometheus", env: map[string]string{EnvPrometheus: "maybe"}},
		{name: "positional args", args: []string{"extra"}},
		{name: "auth user without hash", args: []string{"--auth-user", "admin"}},
		{name: "auth hash without user", env: map[string]string{EnvAuthPasswordHash: testHash}},
		{name: "auth user from env, hash flag cleared", args: []string{"--auth-password-hash="},
			env: map[string]string{EnvAuthUser: "admin", EnvAuthPasswordHash: testHash}},
		{name: "plain password as auth hash", env: map[string]string{EnvAuthUser: "admin", EnvAuthPasswordHash: "hunter2"}},
		{name: "colon in auth user", args: []string{"--auth-user", "ad:min", "--auth-password-hash", testHash}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(tt.args, envFrom(tt.env), io.Discard); err == nil {
				t.Fatal("Load() error = nil, want an error")
			}
		})
	}
}

func TestLoadHelp(t *testing.T) {
	_, err := Load([]string{"--help"}, envFrom(nil), io.Discard)
	if !errors.Is(err, ErrHelp) {
		t.Fatalf("Load(--help) error = %v, want ErrHelp", err)
	}
}

func TestLoadVersion(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "flag", args: []string{"--version"}},
		{name: "single dash", args: []string{"-version"}},
		{name: "with other flags", args: []string{"--listen", ":9090", "--version"}},
		// A broken environment file must not hide the version.
		{name: "bad env", args: []string{"--version"}, env: map[string]string{EnvInterval: "soon"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.args, envFrom(tt.env), io.Discard)
			if !errors.Is(err, ErrVersion) {
				t.Fatalf("Load(%v) error = %v, want ErrVersion", tt.args, err)
			}
		})
	}
}
