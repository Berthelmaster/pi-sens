package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

const (
	servicesJSONCmd  = "systemctl list-units --type=service --all --output=json"
	servicesPlainCmd = "systemctl list-units --type=service --all --plain --no-legend --no-pager"
)

func servicesFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/services/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var servicesT0 = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

func servicesEnv(runner metrics.CommandRunner, offset time.Duration) *metrics.Env {
	return &metrics.Env{Runner: runner, Now: servicesT0.Add(offset)}
}

func TestServicesJSON(t *testing.T) {
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		servicesJSONCmd: {Output: servicesFixture(t, "list-units.json")},
	}}
	s := &services{}
	s.setWatch([]string{"ssh", "dphys-swapfile.service", "nfs-server", "nginx"})

	res, err := s.Collect(context.Background(), servicesEnv(runner, 0))
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	want := servicesData{
		Available: true,
		// nfs-server.service is not-found and not counted.
		Active: 10, Failed: 2, Total: 16,
		FailedUnits: []servicesUnit{
			{Unit: "dphys-swapfile.service", Load: "loaded", Active: "failed", Sub: "failed",
				Description: "dphys-swapfile - set up, mount/unmount, and delete a swap file"},
			{Unit: "wpa_supplicant.service", Load: "loaded", Active: "failed", Sub: "failed", Description: "WPA supplicant"},
		},
		Watched: []servicesUnit{
			{Unit: "ssh.service", Load: "loaded", Active: "active", Sub: "running", Description: "OpenBSD Secure Shell server"},
			{Unit: "dphys-swapfile.service", Load: "loaded", Active: "failed", Sub: "failed",
				Description: "dphys-swapfile - set up, mount/unmount, and delete a swap file"},
			{Unit: "nfs-server.service", Load: "not-found", Active: "inactive", Sub: "dead", Description: "nfs-server.service"},
			{Unit: "nginx.service", Load: "not-found", Active: "inactive", Sub: "dead"},
		},
	}
	if got := res.Data.(servicesData); !reflect.DeepEqual(got, want) {
		t.Errorf("Data = %+v\nwant %+v", got, want)
	}
	if wantSeries := map[string]float64{"services.failed": 2}; !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}
}

func TestServicesEncoding(t *testing.T) {
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		servicesJSONCmd: {Output: `[{"unit":"ssh.service","load":"loaded","active":"active","sub":"running","description":"OpenBSD Secure Shell server"}]`},
	}}
	res, err := (&services{}).Collect(context.Background(), servicesEnv(runner, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	// Empty lists are [] rather than null.
	want := `{"available":true,"active":1,"failed":0,"total":1,"failed_units":[],"watched":[]}`
	if string(b) != want {
		t.Errorf("JSON = %s\nwant %s", b, want)
	}
	if res.Series["services.failed"] != 0 {
		t.Errorf("services.failed = %v, want 0", res.Series["services.failed"])
	}
}

func TestServicesPlainFallback(t *testing.T) {
	wantFailed := []servicesUnit{{
		Unit: "dphys-swapfile.service", Load: "loaded", Active: "failed", Sub: "failed",
		Description: "dphys-swapfile - set up, mount/unmount, and delete a swap file",
	}}
	tests := map[string]map[string]metrics.FakeResponse{
		// A systemctl that rejects --output=json.
		"json rejected": {
			servicesJSONCmd:  {Err: errors.New("systemctl: exit status 1: Unknown output 'json'")},
			servicesPlainCmd: {Output: servicesFixture(t, "list-units-plain.txt")},
		},
		// systemd before 246 accepts --output=json and prints the table anyway.
		"json ignored": {
			servicesJSONCmd:  {Output: servicesFixture(t, "list-units-table.txt")},
			servicesPlainCmd: {Output: servicesFixture(t, "list-units-plain.txt")},
		},
	}
	for name, responses := range tests {
		t.Run(name, func(t *testing.T) {
			runner := &metrics.FakeRunner{Responses: responses}
			s := &services{}
			s.setWatch([]string{"cron", "nfs-server"})

			for i, offset := range []time.Duration{0, time.Minute} {
				res, err := s.Collect(context.Background(), servicesEnv(runner, offset))
				if err != nil {
					t.Fatalf("Collect() #%d error = %v", i+1, err)
				}
				got := res.Data.(servicesData)
				if got.Active != 4 || got.Failed != 1 || got.Total != 6 {
					t.Errorf("#%d counts = active %d, failed %d, total %d; want 4, 1, 6",
						i+1, got.Active, got.Failed, got.Total)
				}
				if !reflect.DeepEqual(got.FailedUnits, wantFailed) {
					t.Errorf("#%d FailedUnits = %+v, want %+v", i+1, got.FailedUnits, wantFailed)
				}
				wantWatched := []servicesUnit{
					{Unit: "cron.service", Load: "loaded", Active: "active", Sub: "running",
						Description: "Regular background program processing daemon"},
					{Unit: "nfs-server.service", Load: "not-found", Active: "inactive", Sub: "dead",
						Description: "nfs-server.service"},
				}
				if !reflect.DeepEqual(got.Watched, wantWatched) {
					t.Errorf("#%d Watched = %+v, want %+v", i+1, got.Watched, wantWatched)
				}
				if res.Series["services.failed"] != 1 {
					t.Errorf("#%d services.failed = %v, want 1", i+1, res.Series["services.failed"])
				}
			}

			// Once JSON turned out not to work, it is not asked for again.
			calls := runner.Calls()
			if last := calls[len(calls)-1]; last != servicesPlainCmd {
				t.Errorf("ran %v, want the second sample to use the plain table", calls)
			}
			if n := strings.Count(strings.Join(calls, "\n"), servicesJSONCmd); n != 1 {
				t.Errorf("ran %v, want JSON tried once", calls)
			}
		})
	}
}

func TestServicesRunsAtMostEvery30s(t *testing.T) {
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		servicesJSONCmd: {Output: servicesFixture(t, "list-units.json")},
	}}
	s := &services{}
	steps := []struct {
		offset time.Duration
		runs   int
	}{
		{0, 1},
		{2 * time.Second, 1},
		{29 * time.Second, 1},
		{30 * time.Second, 2},
		{32 * time.Second, 2},
		{61 * time.Second, 3},
	}
	for _, st := range steps {
		res, err := s.Collect(context.Background(), servicesEnv(runner, st.offset))
		if err != nil {
			t.Fatalf("Collect() at +%v error = %v", st.offset, err)
		}
		// Samples in between repeat the last result, series included.
		if res.Data.(servicesData).Failed != 2 || res.Series["services.failed"] != 2 {
			t.Errorf("at +%v: Data = %+v, Series = %v", st.offset, res.Data, res.Series)
		}
		if n := len(runner.Calls()); n != st.runs {
			t.Errorf("at +%v: systemctl ran %d times, want %d", st.offset, n, st.runs)
		}
	}

	// A new watch list is picked up on the next sample.
	s.setWatch([]string{"ssh"})
	res, err := s.Collect(context.Background(), servicesEnv(runner, 62*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if w := res.Data.(servicesData).Watched; len(w) != 1 || w[0].Active != "active" {
		t.Errorf("Watched = %+v, want ssh.service active", w)
	}
	if n := len(runner.Calls()); n != 4 {
		t.Errorf("systemctl ran %d times, want 4 after changing the watch list", n)
	}
}

func TestServicesUnavailable(t *testing.T) {
	tests := map[string]metrics.FakeResponse{
		"binary missing": {Err: fmt.Errorf("systemctl: %w", &exec.Error{Name: "systemctl", Err: exec.ErrNotFound})},
		"container without systemd": {Err: errors.New("systemctl: exit status 1: " +
			"System has not been booted with systemd as init system (PID 1). Can't operate.\n" +
			"Failed to connect to bus: Host is down")},
		"no bus": {Err: errors.New("systemctl: exit status 1: " +
			"Failed to connect to system scope bus via local transport: No such file or directory")},
	}
	for name, resp := range tests {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{servicesJSONCmd: resp}}
			s := &services{}
			for i := 0; i < 3; i++ {
				res, err := s.Collect(context.Background(), servicesEnv(runner, time.Duration(i)*time.Minute))
				if err != nil {
					t.Fatalf("Collect() #%d error = %v, want an unavailable section", i+1, err)
				}
				b, _ := json.Marshal(res.Data)
				if string(b) != `{"available":false}` {
					t.Errorf("Data = %s, want {\"available\":false}", b)
				}
				if len(res.Series) != 0 {
					t.Errorf("Series = %v, want none", res.Series)
				}
			}
			if calls := runner.Calls(); len(calls) != 1 {
				t.Errorf("ran %v, want systemctl to be tried once", calls)
			}
			if n := strings.Count(logs.String(), "\n"); n != 1 {
				t.Errorf("logged %d lines, want 1:\n%s", n, logs)
			}
		})
	}
}

func TestServicesUnavailableWithExecRunner(t *testing.T) {
	// The real runner's error for a missing binary must be recognised too.
	t.Setenv("PATH", t.TempDir())
	captureLog(t)
	res, err := (&services{}).Collect(context.Background(), servicesEnv(metrics.ExecRunner{}, 0))
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if _, ok := res.Data.(servicesUnavailable); !ok {
		t.Errorf("Data = %+v, want the unavailable section", res.Data)
	}
}

func TestServicesErrors(t *testing.T) {
	tests := map[string]map[string]metrics.FakeResponse{
		"bad json":            {servicesJSONCmd: {Output: `[{"unit":`}},
		"unit without a name": {servicesJSONCmd: {Output: `[{"load":"loaded"}]`}},
		"unexpected output":   {servicesJSONCmd: {Output: "something else entirely\n"}},
		"both commands fail": {
			servicesJSONCmd:  {Err: errors.New("systemctl: exit status 1: Access denied")},
			servicesPlainCmd: {Err: errors.New("systemctl: exit status 1: Access denied")},
		},
	}
	for name, responses := range tests {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			runner := &metrics.FakeRunner{Responses: responses}
			s := &services{}
			if _, err := s.Collect(context.Background(), servicesEnv(runner, 0)); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
			runs := len(runner.Calls())
			// The error is repeated until the next run is due...
			if _, err := s.Collect(context.Background(), servicesEnv(runner, 10*time.Second)); err == nil {
				t.Fatal("Collect() error = nil on a cached sample, want the same error")
			}
			if n := len(runner.Calls()); n != runs {
				t.Errorf("systemctl ran again within 30s: %v", runner.Calls())
			}
			// ...and then systemctl is tried again, not given up on.
			if _, err := s.Collect(context.Background(), servicesEnv(runner, 30*time.Second)); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
			if n := len(runner.Calls()); n == runs {
				t.Errorf("systemctl was not retried after 30s: %v", runner.Calls())
			}
			if logs.Len() != 0 {
				t.Errorf("logged %q, want nothing (the sampler logs collector errors)", logs)
			}
		})
	}
}

func TestServicesCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &metrics.FakeRunner{}
	s := &services{}
	if _, err := s.Collect(ctx, servicesEnv(runner, 0)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
	// Neither cached nor mistaken for a missing systemctl.
	runner.Responses = map[string]metrics.FakeResponse{servicesJSONCmd: {Output: "[]"}}
	res, err := s.Collect(context.Background(), servicesEnv(runner, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Data.(servicesData).Available {
		t.Error("Available = false after a canceled call, want true")
	}
}

func TestServicesSetWatch(t *testing.T) {
	s := &services{}
	s.setWatch([]string{" ssh ", "", "pisens.service", "ssh.service", "getty@tty1", "org.cups.cupsd"})
	want := []string{"ssh.service", "pisens.service", "getty@tty1.service", "org.cups.cupsd.service"}
	if !reflect.DeepEqual(s.watch, want) {
		t.Errorf("watch = %v, want %v", s.watch, want)
	}
}

func TestServicesIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "services" {
			return
		}
	}
	t.Fatal("services collector is not registered")
}
