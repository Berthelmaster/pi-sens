package config

import (
	"errors"
	"io"
	"reflect"
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
			want: Config{Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second},
		},
		{
			name: "env only",
			env: map[string]string{
				EnvListen:   "127.0.0.1:9000",
				EnvInterval: "500ms",
				EnvDataDir:  "/var/lib/pisens",

				EnvHistoryInterval: "30s",
			},
			want: Config{Listen: "127.0.0.1:9000", Interval: 500 * time.Millisecond, DataDir: "/var/lib/pisens", HistoryInterval: 30 * time.Second},
		},
		{
			name: "flags only",
			args: []string{"--listen", ":9090", "--interval=5s", "-data-dir", "/tmp/pisens", "--history-interval", "1m"},
			want: Config{Listen: ":9090", Interval: 5 * time.Second, DataDir: "/tmp/pisens", HistoryInterval: time.Minute},
		},
		{
			name: "flags win over env",
			args: []string{"--listen", ":9090", "--interval", "1m", "--history-interval", "20s"},
			env: map[string]string{
				EnvListen:   ":7070",
				EnvInterval: "10s",
				EnvDataDir:  "/from/env",

				EnvHistoryInterval: "5m",
			},
			want: Config{Listen: ":9090", Interval: time.Minute, DataDir: "/from/env", HistoryInterval: 20 * time.Second},
		},
		{
			name: "watch services from env",
			env:  map[string]string{EnvWatchServices: " ssh, pisens.service,,nginx "},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				WatchServices: []string{"ssh", "pisens.service", "nginx"},
			},
		},
		{
			name: "watch services flag wins over env",
			args: []string{"--watch-services", "cron"},
			env:  map[string]string{EnvWatchServices: "ssh,nginx"},
			want: Config{
				Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second,
				WatchServices: []string{"cron"},
			},
		},
		{
			name: "empty watch services flag clears env",
			args: []string{"--watch-services="},
			env:  map[string]string{EnvWatchServices: "ssh"},
			want: Config{Listen: ":8080", Interval: 2 * time.Second, DataDir: "./data", HistoryInterval: 10 * time.Second},
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
		{name: "empty listen flag", args: []string{"--listen", ""}},
		{name: "empty data dir flag", args: []string{"--data-dir="}},
		{name: "unknown flag", args: []string{"--nope"}},
		{name: "space in watched service", args: []string{"--watch-services", "ssh,my service"}},
		{name: "path as watched service", env: map[string]string{EnvWatchServices: "/etc/systemd/ssh.service"}},
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
