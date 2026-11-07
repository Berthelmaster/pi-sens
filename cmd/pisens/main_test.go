package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
	"github.com/Berthelmaster/pi-sens/internal/server"
	"github.com/Berthelmaster/pi-sens/web"
)

// TestMetricsEndpoint wires the registered collectors, the sampler and the
// server together the way run does, with the Pi 5 sysinfo fixture as the root FS.
func TestMetricsEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sampler := metrics.NewSampler(metrics.Options{
		Interval: time.Second,
		FS:       os.DirFS("../../internal/collectors/testdata/sysinfo/pi5"),
		Runner:   &metrics.FakeRunner{},
		Logger:   logger,
	})
	sampler.Sample(context.Background())
	srv := server.New(":0", "dev", web.Dist(), logger, sampler, nil, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body)
	}

	var body struct {
		Timestamp  time.Time                  `json:"timestamp"`
		Collectors map[string]json.RawMessage `json:"collectors"`
		Series     map[string]float64         `json:"series"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Timestamp.IsZero() {
		t.Error("timestamp is zero")
	}
	var sys struct {
		UptimeS float64 `json:"uptime_s"`
		Error   string  `json:"error"`
	}
	if err := json.Unmarshal(body.Collectors["sysinfo"], &sys); err != nil {
		t.Fatalf("decode sysinfo section %s: %v", body.Collectors["sysinfo"], err)
	}
	if sys.Error != "" || sys.UptimeS != 354127.42 {
		t.Errorf("sysinfo section = %s, want uptime_s 354127.42", body.Collectors["sysinfo"])
	}
	if got := body.Series["system.uptime_s"]; got != 354127.42 {
		t.Errorf("series system.uptime_s = %v, want 354127.42", got)
	}
}

// TestRunRejectsInvalidAlertsFile checks that a broken rules file stops
// startup with exit code 2 and a message that points at the problem.
func TestRunRejectsInvalidAlertsFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "alerts.yaml")
	rules := "rules:\n  - name: disk\n    series: fs.*.used_pct\n    op: \">=\"\n    threshold: 90\n    severity: warning\n"
	if err := os.WriteFile(bad, []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{
			name: "flag",
			args: []string{"--listen", "127.0.0.1:0", "--alerts-file", bad},
			want: "pisens: alerts file " + bad + `: rule 1 "disk" (line 2): op must be ">" or "<", got ">="`,
		},
		{
			name: "env",
			args: []string{"--listen", "127.0.0.1:0"},
			env:  map[string]string{"PISENS_ALERTS_FILE": bad},
			want: "pisens: alerts file " + bad + ": rule 1",
		},
		{
			name: "missing file",
			args: []string{"--listen", "127.0.0.1:0", "--alerts-file", filepath.Join(dir, "nope.yaml")},
			want: "pisens: alerts file: open " + filepath.Join(dir, "nope.yaml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr strings.Builder
			code := run(tt.args, func(k string) string { return tt.env[k] }, &stderr)
			if code != 2 {
				t.Errorf("run() = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.want)
			}
		})
	}
}

// TestRunRefusesHalfConfiguredAuth checks that pisens does not start, open
// or otherwise, when only one of the two auth settings is given.
func TestRunRefusesHalfConfiguredAuth(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{
			name: "user only",
			args: []string{"--listen", "127.0.0.1:0", "--auth-user", "admin"},
			want: "auth user is set without an auth password hash",
		},
		{
			name: "hash only",
			args: []string{"--listen", "127.0.0.1:0"},
			env:  map[string]string{"PISENS_AUTH_PASSWORD_HASH": "$2a$10$azHQ.jqYzZIyXkRa.ETdgeauf8sdkH.P0fxNsJ6DnqpKRaQXd7dzG"},
			want: "auth password hash is set without an auth user",
		},
		{
			name: "plain password",
			args: []string{"--listen", "127.0.0.1:0", "--auth-user", "admin", "--auth-password-hash", "hunter2"},
			want: "not a bcrypt hash",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr strings.Builder
			code := run(tt.args, func(k string) string { return tt.env[k] }, &stderr)
			if code != 2 {
				t.Errorf("run() = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.want)
			}
			if strings.Contains(stderr.String(), "hunter2") {
				t.Errorf("stderr repeats the value given as the hash: %q", stderr.String())
			}
		})
	}
}

func TestHashPassword(t *testing.T) {
	tests := []struct {
		name     string
		stdin    string
		password string // what the printed hash must match
	}{
		{name: "trailing newline", stdin: "correct horse\n", password: "correct horse"},
		{name: "crlf", stdin: "correct horse\r\n", password: "correct horse"},
		{name: "no newline", stdin: "correct horse", password: "correct horse"},
		{name: "spaces kept", stdin: " pw \n", password: " pw "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := runHashPassword(nil, strings.NewReader(tt.stdin), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr %q", code, stderr.String())
			}
			hash := strings.TrimSuffix(stdout.String(), "\n")
			if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(tt.password)); err != nil {
				t.Errorf("printed hash %q does not match %q: %v", hash, tt.password, err)
			}
		})
	}

	failures := []struct {
		name  string
		args  []string
		stdin string
		code  int
		want  string
	}{
		{name: "empty", stdin: "\n", code: 1, want: "must not be empty"},
		{name: "two lines", stdin: "one\ntwo\n", code: 1, want: "single line"},
		{name: "too long for bcrypt", stdin: strings.Repeat("x", 73), code: 1, want: "hash password"},
		{name: "huge input", stdin: strings.Repeat("x", 10000), code: 1, want: "longer than"},
		{name: "extra argument", args: []string{"pw"}, code: 2, want: "unexpected arguments"},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := runHashPassword(tt.args, strings.NewReader(tt.stdin), &stdout, &stderr)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d", code, tt.code)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.want)
			}
		})
	}

	var stdout, stderr strings.Builder
	if code := runHashPassword([]string{"--help"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Errorf("--help exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "Usage: pisens hash-password") {
		t.Errorf("--help output = %q", stderr.String())
	}
}
