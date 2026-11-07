package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
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
			// The runner has no answer for systemctl show, so nginx falls back.
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

// servicesShowCmd is the systemctl show line for the given unit names.
func servicesShowCmd(names ...string) string {
	return "systemctl show --property=Id,LoadState,ActiveState,SubState,Description " + strings.Join(names, " ")
}

func TestServicesShowsUnlistedWatchedUnits(t *testing.T) {
	showCmd := servicesShowCmd("nginx.service", "sshd.service", "nginxx.service")
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		servicesJSONCmd: {Output: servicesFixture(t, "list-units.json")},
		showCmd:         {Output: servicesFixture(t, "show.txt")},
	}}
	s := &services{}
	// nginx is installed but stopped and unloaded, sshd is an alias of
	// ssh.service and nginxx is a typo.
	s.setWatch([]string{"nginx", "ssh", "sshd", "nfs-server", "nginxx"})

	res, err := s.Collect(context.Background(), servicesEnv(runner, 0))
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	got := res.Data.(servicesData)
	wantWatched := []servicesUnit{
		{Unit: "nginx.service", Load: "loaded", Active: "inactive", Sub: "dead",
			Description: "A high performance web server and a reverse proxy server"},
		{Unit: "ssh.service", Load: "loaded", Active: "active", Sub: "running", Description: "OpenBSD Secure Shell server"},
		{Unit: "sshd.service", Load: "loaded", Active: "active", Sub: "running", Description: "OpenBSD Secure Shell server"},
		// Listed by list-units, so not asked for again.
		{Unit: "nfs-server.service", Load: "not-found", Active: "inactive", Sub: "dead", Description: "nfs-server.service"},
		{Unit: "nginxx.service", Load: "not-found", Active: "inactive", Sub: "dead", Description: "nginxx.service"},
	}
	if !reflect.DeepEqual(got.Watched, wantWatched) {
		t.Errorf("Watched = %+v\nwant %+v", got.Watched, wantWatched)
	}
	// The counts only cover what list-units reports.
	if got.Active != 10 || got.Failed != 2 || got.Total != 16 {
		t.Errorf("counts = active %d, failed %d, total %d; want 10, 2, 16", got.Active, got.Failed, got.Total)
	}
	if want := []string{servicesJSONCmd, showCmd}; !reflect.DeepEqual(runner.Calls(), want) {
		t.Errorf("ran %q, want %q", runner.Calls(), want)
	}

	// show is part of the 30 s refresh, not run on every sample.
	if _, err := s.Collect(context.Background(), servicesEnv(runner, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Collect(context.Background(), servicesEnv(runner, 30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if want := []string{servicesJSONCmd, showCmd, servicesJSONCmd, showCmd}; !reflect.DeepEqual(runner.Calls(), want) {
		t.Errorf("ran %q, want %q", runner.Calls(), want)
	}
}

func TestServicesNoShowWhenNothingMissing(t *testing.T) {
	for name, watch := range map[string][]string{
		"no watch list": nil,
		// nfs-server is listed even though it is not-found.
		"all listed": {"ssh", "dphys-swapfile", "nfs-server"},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
				servicesJSONCmd: {Output: servicesFixture(t, "list-units.json")},
			}}
			s := &services{}
			s.setWatch(watch)
			res, err := s.Collect(context.Background(), servicesEnv(runner, 0))
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if w := res.Data.(servicesData).Watched; len(w) != len(watch) {
				t.Errorf("Watched = %+v, want %d units", w, len(watch))
			}
			if want := []string{servicesJSONCmd}; !reflect.DeepEqual(runner.Calls(), want) {
				t.Errorf("ran %q, want %q", runner.Calls(), want)
			}
		})
	}
}

func TestServicesShowFailureFallsBack(t *testing.T) {
	showCmd := servicesShowCmd("nginx.service", "foo.service")
	tests := map[string]metrics.FakeResponse{
		"command fails": {Err: errors.New("systemctl: exit status 1: Access denied")},
		"too few units": {Output: "Id=nginx.service\nLoadState=loaded\nActiveState=inactive\nSubState=dead\n"},
		"too many units": {Output: "LoadState=loaded\nActiveState=inactive\nSubState=dead\n\n" +
			"LoadState=loaded\nActiveState=inactive\nSubState=dead\n\n" +
			"LoadState=loaded\nActiveState=inactive\nSubState=dead\n"},
		"missing state":   {Output: "Id=nginx.service\nLoadState=loaded\n\nId=foo.service\nLoadState=not-found\n"},
		"unexpected line": {Output: "Unknown operation 'show'.\n"},
		"empty output":    {Output: ""},
	}
	for name, resp := range tests {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
				servicesJSONCmd: {Output: servicesFixture(t, "list-units.json")},
				showCmd:         resp,
			}}
			s := &services{}
			s.setWatch([]string{"ssh", "nginx", "foo"})
			res, err := s.Collect(context.Background(), servicesEnv(runner, 0))
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			want := []servicesUnit{
				{Unit: "ssh.service", Load: "loaded", Active: "active", Sub: "running", Description: "OpenBSD Secure Shell server"},
				{Unit: "nginx.service", Load: "not-found", Active: "inactive", Sub: "dead"},
				{Unit: "foo.service", Load: "not-found", Active: "inactive", Sub: "dead"},
			}
			if got := res.Data.(servicesData); !reflect.DeepEqual(got.Watched, want) {
				t.Errorf("Watched = %+v\nwant %+v", got.Watched, want)
			}
			if wantCalls := []string{servicesJSONCmd, showCmd}; !reflect.DeepEqual(runner.Calls(), wantCalls) {
				t.Errorf("ran %q, want %q", runner.Calls(), wantCalls)
			}
			if logs.Len() != 0 {
				t.Errorf("logged %q, want nothing at info level", logs)
			}
		})
	}
}

// servicesCancelOnShow cancels the sample's context while systemctl show runs.
type servicesCancelOnShow struct {
	metrics.FakeRunner
	cancel context.CancelFunc
}

func (r *servicesCancelOnShow) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "show" {
		r.cancel()
	}
	return r.FakeRunner.Run(ctx, name, args...)
}

func TestServicesShowCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &servicesCancelOnShow{cancel: cancel}
	runner.Responses = map[string]metrics.FakeResponse{
		servicesJSONCmd:                  {Output: servicesFixture(t, "list-units.json")},
		servicesShowCmd("nginx.service"): {Output: "LoadState=loaded\nActiveState=inactive\nSubState=dead\n"},
	}
	s := &services{}
	s.setWatch([]string{"nginx"})
	if _, err := s.Collect(ctx, servicesEnv(runner, 0)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
	// Not cached: the next sample runs systemctl again and gets the state.
	res, err := s.Collect(context.Background(), servicesEnv(runner, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if w := res.Data.(servicesData).Watched; len(w) != 1 || w[0].Load != "loaded" {
		t.Errorf("Watched = %+v, want nginx.service loaded", w)
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

// servicesBusErr is what systemctl prints while the system D-Bus is down.
var servicesBusErr = errors.New("systemctl: exit status 1: " +
	"Failed to connect to bus: No such file or directory")

// servicesSystemdFS is the root of a machine booted with systemd.
var servicesSystemdFS = fstest.MapFS{"run/systemd/system": {Mode: fs.ModeDir}}

func TestServicesBusErrorOnSystemdHostIsRetried(t *testing.T) {
	logs := captureLog(t)
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		servicesJSONCmd:  {Err: servicesBusErr},
		servicesPlainCmd: {Err: servicesBusErr},
	}}
	s := &services{}
	env := func(offset time.Duration) *metrics.Env {
		e := servicesEnv(runner, offset)
		e.FS = servicesSystemdFS
		return e
	}
	if _, err := s.Collect(context.Background(), env(0)); err == nil || !strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Fatalf("Collect() error = %v, want the bus error", err)
	}
	runs := len(runner.Calls())
	// The error is repeated until the next run is due...
	if _, err := s.Collect(context.Background(), env(10*time.Second)); err == nil {
		t.Fatal("Collect() error = nil on a cached sample, want the bus error")
	}
	if n := len(runner.Calls()); n != runs {
		t.Errorf("systemctl ran again within 30s: %v", runner.Calls())
	}

	// ...and once D-Bus is back, the next run 30 s later recovers.
	runner.Responses = map[string]metrics.FakeResponse{
		servicesJSONCmd: {Output: servicesFixture(t, "list-units.json")},
	}
	res, err := s.Collect(context.Background(), env(30*time.Second))
	if err != nil {
		t.Fatalf("Collect() after the bus came back error = %v", err)
	}
	if n := len(runner.Calls()); n == runs {
		t.Errorf("systemctl was not retried after 30s: %v", runner.Calls())
	}
	if d, ok := res.Data.(servicesData); !ok || !d.Available || d.Failed != 2 {
		t.Errorf("Data = %+v, want the services from list-units.json", res.Data)
	}
	if res.Series["services.failed"] != 2 {
		t.Errorf("Series = %v, want services.failed 2", res.Series)
	}
	if logs.Len() != 0 {
		t.Errorf("logged %q, want nothing (the sampler logs collector errors)", logs)
	}
}

func TestServicesBusErrorWithoutSystemdIsUnavailable(t *testing.T) {
	tests := map[string]fs.FS{
		"no run/systemd/system":      fstest.MapFS{"run/lock": {Mode: fs.ModeDir}},
		"run/systemd/system is file": fstest.MapFS{"run/systemd/system": {Data: []byte("x")}},
		"no filesystem":              nil,
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			captureLog(t)
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
				servicesJSONCmd: {Err: servicesBusErr},
			}}
			s := &services{}
			for i := 0; i < 3; i++ {
				e := servicesEnv(runner, time.Duration(i)*time.Minute)
				e.FS = fsys
				res, err := s.Collect(context.Background(), e)
				if err != nil {
					t.Fatalf("Collect() #%d error = %v, want an unavailable section", i+1, err)
				}
				if _, ok := res.Data.(servicesUnavailable); !ok {
					t.Errorf("Data = %+v, want the unavailable section", res.Data)
				}
			}
			if calls := runner.Calls(); len(calls) != 1 {
				t.Errorf("ran %v, want systemctl to be tried once", calls)
			}
		})
	}
}

func TestServicesNotBootedWithSystemdIsUnavailableOnAnyHost(t *testing.T) {
	// systemctl's own verdict wins over /run/systemd/system, e.g. in a
	// container that has the host's /run mounted.
	captureLog(t)
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		servicesJSONCmd: {Err: errors.New("systemctl: exit status 1: " +
			"System has not been booted with systemd as init system (PID 1). Can't operate.\n" +
			"Failed to connect to bus: Host is down")},
	}}
	e := servicesEnv(runner, 0)
	e.FS = servicesSystemdFS
	res, err := (&services{}).Collect(context.Background(), e)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if _, ok := res.Data.(servicesUnavailable); !ok {
		t.Errorf("Data = %+v, want the unavailable section", res.Data)
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
