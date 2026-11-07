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
