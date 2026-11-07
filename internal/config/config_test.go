package config

import (
	"errors"
	"io"
	"testing"
	"time"
)

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
			},
			want: Config{
				Listen: "127.0.0.1:9000", Interval: 500 * time.Millisecond, DataDir: "/var/lib/pisens", HistoryInterval: 30 * time.Second,
				HistoryRawRetention: 12 * time.Hour, History1mRetention: 72 * time.Hour, History15mRetention: 720 * time.Hour,
			},
		},
		{
			name: "flags only",
			args: []string{
				"--listen", ":9090", "--interval=5s", "-data-dir", "/tmp/pisens", "--history-interval", "1m",
				"--history-raw-retention", "6h", "--history-1m-retention=48h", "--history-15m-retention", "8760h",
			},
			want: Config{
				Listen: ":9090", Interval: 5 * time.Second, DataDir: "/tmp/pisens", HistoryInterval: time.Minute,
				HistoryRawRetention: 6 * time.Hour, History1mRetention: 48 * time.Hour, History15mRetention: 8760 * time.Hour,
			},
		},
		{
			name: "flags win over env",
			args: []string{"--listen", ":9090", "--interval", "1m", "--history-interval", "20s", "--history-raw-retention", "2h"},
			env: map[string]string{
				EnvListen:   ":7070",
				EnvInterval: "10s",
				EnvDataDir:  "/from/env",

				EnvHistoryInterval: "5m",

				EnvHistoryRawRetention: "1h",
				EnvHistory1mRetention:  "24h",
			},
			want: Config{
				Listen: ":9090", Interval: time.Minute, DataDir: "/from/env", HistoryInterval: 20 * time.Second,
				HistoryRawRetention: 2 * time.Hour, History1mRetention: 24 * time.Hour, History15mRetention: DefaultHistory15mRetention,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(tt.args, envFrom(tt.env), io.Discard)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got != tt.want {
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
		{name: "unknown flag", args: []string{"--nope"}},
		{name: "positional args", args: []string{"extra"}},
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
