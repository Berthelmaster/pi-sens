package collectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func TestThrottle(t *testing.T) {
	tests := []struct {
		out        string
		raw        string
		flags      throttleFlags
		throttled  float64
		underVolts float64
	}{
		{out: "throttled=0x0\n", raw: "0x0"},
		{
			// Under-voltage, capping and throttling happened earlier but
			// are over now.
			out: "throttled=0x50000\n", raw: "0x50000",
			flags: throttleFlags{UnderVoltageOccurred: true, ThrottledOccurred: true},
		},
		{
			// A weak power supply: under-voltage right now and throttled.
			out: "throttled=0x50005\n", raw: "0x50005",
			flags: throttleFlags{
				UnderVoltage: true, Throttled: true,
				UnderVoltageOccurred: true, ThrottledOccurred: true,
			},
			throttled: 1, underVolts: 1,
		},
		{
			// Hot but well powered: only the soft temperature limit.
			out: "throttled=0x80008\n", raw: "0x80008",
			flags:     throttleFlags{SoftTempLimit: true, SoftTempLimitOccurred: true},
			throttled: 1,
		},
		{
			out: "throttled=0xF000F", raw: "0xf000f",
			flags: throttleFlags{
				UnderVoltage: true, ArmFreqCapped: true, Throttled: true, SoftTempLimit: true,
				UnderVoltageOccurred: true, ArmFreqCappedOccurred: true,
				ThrottledOccurred: true, SoftTempLimitOccurred: true,
			},
			throttled: 1, underVolts: 1,
		},
		{
			// Arm frequency capping alone counts as currently throttled.
			out: "throttled=0x2\n", raw: "0x2",
			flags:     throttleFlags{ArmFreqCapped: true},
			throttled: 1,
		},
	}
	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.out), func(t *testing.T) {
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
				"vcgencmd get_throttled": {Output: tt.out},
			}}
			res, err := throttle{}.Collect(context.Background(), &metrics.Env{Runner: runner})
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			flags := tt.flags
			want := throttleData{Available: true, Raw: tt.raw, Flags: &flags}
			if got := res.Data.(throttleData); !reflect.DeepEqual(got, want) {
				t.Errorf("Data = %+v (flags %+v)\nwant %+v (flags %+v)", got, got.Flags, want, want.Flags)
			}
			wantSeries := map[string]float64{"pi.throttled": tt.throttled, "pi.undervoltage": tt.underVolts}
			if !reflect.DeepEqual(res.Series, wantSeries) {
				t.Errorf("Series = %v, want %v", res.Series, wantSeries)
			}
		})
	}
}

func TestThrottleJSON(t *testing.T) {
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		"vcgencmd get_throttled": {Output: "throttled=0x50005\n"},
	}}
	res, err := throttle{}.Collect(context.Background(), &metrics.Env{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"available":true,"raw":"0x50005","flags":{` +
		`"under_voltage":true,"arm_freq_capped":false,"throttled":true,"soft_temp_limit":false,` +
		`"under_voltage_occurred":true,"arm_freq_capped_occurred":false,` +
		`"throttled_occurred":true,"soft_temp_limit_occurred":false}}`
	if string(b) != want {
		t.Errorf("JSON = %s\nwant %s", b, want)
	}
}

// captureLog sends the default slog logger to a buffer for the rest of the
// test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestThrottleUnavailable(t *testing.T) {
	tests := map[string]metrics.FakeResponse{
		"binary missing": {Err: fmt.Errorf("vcgencmd: %w", &exec.Error{Name: "vcgencmd", Err: exec.ErrNotFound})},
		"no permission on /dev/vcio": {
			Err: errors.New("vcgencmd: exit status 255: Can't open device file: /dev/vcio"),
		},
		"old firmware interface": {Err: errors.New("vcgencmd: exit status 255: VCHI initialization failed")},
	}
	for name, resp := range tests {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
				"vcgencmd get_throttled": resp,
			}}
			env := &metrics.Env{Runner: runner}

			for i := 0; i < 3; i++ {
				res, err := throttle{}.Collect(context.Background(), env)
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

			if n := strings.Count(logs.String(), "\n"); n != 1 {
				t.Errorf("logged %d lines, want 1:\n%s", n, logs)
			}
			if !strings.Contains(logs.String(), "video group") {
				t.Errorf("log does not mention the video group:\n%s", logs)
			}
			if calls := runner.Calls(); len(calls) != 1 {
				t.Errorf("ran %v, want vcgencmd to be tried once", calls)
			}
		})
	}
}

func TestThrottleErrors(t *testing.T) {
	tests := map[string]metrics.FakeResponse{
		"unexpected output": {Output: "error=1 error_msg=\"Command not registered\"\n"},
		"not a number":      {Output: "throttled=0xzz\n"},
		"too large":         {Output: "throttled=0x100000000\n"},
		"other failure":     {Err: errors.New("vcgencmd: exit status 1")},
	}
	for name, resp := range tests {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
				"vcgencmd get_throttled": resp,
			}}
			env := &metrics.Env{Runner: runner}
			for i := 0; i < 2; i++ {
				if _, err := (throttle{}).Collect(context.Background(), env); err == nil {
					t.Fatal("Collect() error = nil, want an error")
				}
			}
			// A transient failure must not disable vcgencmd for good.
			if calls := runner.Calls(); len(calls) != 2 {
				t.Errorf("ran %v, want vcgencmd to be retried", calls)
			}
			if logs.Len() != 0 {
				t.Errorf("logged %q, want nothing (the sampler logs collector errors)", logs)
			}
		})
	}
}

func TestThrottleCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &metrics.FakeRunner{}
	env := &metrics.Env{Runner: runner}
	if _, err := (throttle{}).Collect(ctx, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
	// The cancellation must not be mistaken for a missing vcgencmd.
	runner.Responses = map[string]metrics.FakeResponse{"vcgencmd get_throttled": {Output: "throttled=0x0"}}
	res, err := throttle{}.Collect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Data.(throttleData).Available {
		t.Error("Available = false after a canceled call, want true")
	}
}

func TestThrottleIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "throttle" {
			return
		}
	}
	t.Fatal("throttle collector is not registered")
}

func TestVcgencmdMissingWithExecRunner(t *testing.T) {
	// The real runner's error for a missing binary must be recognised too.
	t.Setenv("PATH", t.TempDir())
	captureLog(t)
	_, err := vcgencmd(context.Background(), &metrics.Env{Runner: metrics.ExecRunner{}}, "get_throttled")
	if !errors.Is(err, errVcgencmdUnavailable) {
		t.Fatalf("vcgencmd() error = %v, want errVcgencmdUnavailable", err)
	}
}
