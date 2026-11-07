package collectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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
			now := time.Date(2026, 11, 7, 14, 2, 0, 0, time.UTC)
			env := &metrics.Env{Runner: runner, FS: fstest.MapFS{}, Now: now}
			res, err := (&throttle{}).Collect(context.Background(), env)
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			flags := tt.flags
			// Under-voltage on the first sample counts as an event.
			events := int(tt.underVolts)
			want := throttleData{Available: true, Raw: tt.raw, Flags: &flags, UndervoltageEvents: &events}
			if events > 0 {
				want.LastUndervoltage = &now
			}
			if got := res.Data.(throttleData); !reflect.DeepEqual(got, want) {
				t.Errorf("Data = %+v (flags %+v)\nwant %+v (flags %+v)", got, got.Flags, want, want.Flags)
			}
			wantSeries := map[string]float64{
				"pi.throttled": tt.throttled, "pi.undervoltage": tt.underVolts,
				"power.undervoltage_events": tt.underVolts, "power.undervoltage_events_5m": tt.underVolts,
			}
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
	env := &metrics.Env{
		Runner: runner,
		FS:     os.DirFS("testdata/throttle/psu_3a"),
		Now:    time.Date(2026, 11, 7, 15, 2, 0, 0, time.FixedZone("CET", 3600)),
	}
	res, err := (&throttle{}).Collect(context.Background(), env)
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
		`"throttled_occurred":true,"soft_temp_limit_occurred":false},` +
		`"psu_max_current_ma":3000,"psu_ok":false,"usb_max_current_enable":false,` +
		`"undervoltage_events":1,"last_undervoltage":"2026-11-07T14:02:00Z"}`
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
				res, err := (&throttle{}).Collect(context.Background(), env)
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
				if _, err := (&throttle{}).Collect(context.Background(), env); err == nil {
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
	if _, err := (&throttle{}).Collect(ctx, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
	// The cancellation must not be mistaken for a missing vcgencmd.
	runner.Responses = map[string]metrics.FakeResponse{"vcgencmd get_throttled": {Output: "throttled=0x0"}}
	res, err := (&throttle{}).Collect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Data.(throttleData).Available {
		t.Error("Available = false after a canceled call, want true")
	}
}

func TestThrottlePSU(t *testing.T) {
	u32 := func(v uint32) *uint32 { return &v }
	b := func(v bool) *bool { return &v }
	tests := []struct {
		fixture string
		maxMA   *uint32
		ok      *bool
		usbMax  *bool
	}{
		// The official 27 W supply negotiates 5 A over USB-PD.
		{fixture: "psu_5a", maxMA: u32(5000), ok: b(true), usbMax: b(true)},
		// A plain 5 V 3 A supply: USB ports limited to 600 mA.
		{fixture: "psu_3a", maxMA: u32(3000), ok: b(false), usbMax: b(false)},
		// The same supply with usb_max_current_enable=1 in config.txt.
		{fixture: "psu_3a_usb_max", maxMA: u32(3000), ok: b(false), usbMax: b(true)},
		// Older firmware: the rating but no USB property.
		{fixture: "psu_no_usb", maxMA: u32(3000), ok: b(false)},
		// A Pi 4 has no power node at all.
		{fixture: "psu_missing"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
				"vcgencmd get_throttled": {Output: "throttled=0x0\n"},
			}}
			env := &metrics.Env{Runner: runner, FS: os.DirFS("testdata/throttle/" + tt.fixture)}
			res, err := (&throttle{}).Collect(context.Background(), env)
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			got := res.Data.(throttleData)
			if !reflect.DeepEqual(got.PSUMaxCurrentMA, tt.maxMA) {
				t.Errorf("PSUMaxCurrentMA = %v, want %v", deref(got.PSUMaxCurrentMA), deref(tt.maxMA))
			}
			if !reflect.DeepEqual(got.PSUOK, tt.ok) {
				t.Errorf("PSUOK = %v, want %v", deref(got.PSUOK), deref(tt.ok))
			}
			if !reflect.DeepEqual(got.USBMaxCurrentEnable, tt.usbMax) {
				t.Errorf("USBMaxCurrentEnable = %v, want %v", deref(got.USBMaxCurrentEnable), deref(tt.usbMax))
			}
		})
	}
}

func TestThrottlePSUMalformed(t *testing.T) {
	// A property that is not a single u32 is left out rather than misread.
	fsys := fstest.MapFS{
		psuMaxCurrentFile: {Data: []byte{0x00, 0x13, 0x88}},
		psuUSBMaxFile:     {Data: []byte{0, 0, 0, 0, 0, 0, 0, 1}},
	}
	maxMA, ok, usbMax := psuRating(fsys)
	if maxMA != nil || ok != nil || usbMax != nil {
		t.Errorf("psuRating() = %v, %v, %v, want all nil", deref(maxMA), deref(ok), deref(usbMax))
	}
}

// deref formats a pointer field for test messages.
func deref[T any](p *T) any {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestThrottleUndervoltageEvents(t *testing.T) {
	const (
		on  = "throttled=0x50005\n"
		off = "throttled=0x50000\n"
	)
	start := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)
	steps := []struct {
		at     time.Duration
		out    string
		err    bool // the sample fails
		events int
		recent int
		last   time.Duration
	}{
		{at: 0, out: off},
		{at: 2 * time.Second, out: on, events: 1, recent: 1, last: 2 * time.Second},
		// Staying set is the same event.
		{at: 4 * time.Second, out: on, events: 1, recent: 1, last: 2 * time.Second},
		{at: 6 * time.Second, out: off, events: 1, recent: 1, last: 2 * time.Second},
		{at: 8 * time.Second, out: on, events: 2, recent: 2, last: 8 * time.Second},
		// A failed sample in between neither ends nor starts an event.
		{at: 10 * time.Second, err: true},
		{at: 12 * time.Second, out: on, events: 2, recent: 2, last: 8 * time.Second},
		{at: 14 * time.Second, out: off, events: 2, recent: 2, last: 8 * time.Second},
		// The first event leaves the 5 minute window, then the second.
		{at: 5*time.Minute + 2*time.Second, out: off, events: 2, recent: 1, last: 8 * time.Second},
		{at: 5*time.Minute + 8*time.Second, out: off, events: 2, recent: 0, last: 8 * time.Second},
	}

	runner := &metrics.FakeRunner{}
	c := &throttle{}
	for _, s := range steps {
		resp := metrics.FakeResponse{Output: s.out}
		if s.err {
			resp = metrics.FakeResponse{Err: errors.New("vcgencmd: exit status 1")}
		}
		runner.Responses = map[string]metrics.FakeResponse{"vcgencmd get_throttled": resp}
		env := &metrics.Env{Runner: runner, FS: fstest.MapFS{}, Now: start.Add(s.at)}
		res, err := c.Collect(context.Background(), env)
		if s.err {
			if err == nil {
				t.Fatalf("at %s: Collect() error = nil, want an error", s.at)
			}
			continue
		}
		if err != nil {
			t.Fatalf("at %s: Collect() error = %v", s.at, err)
		}
		if got := res.Series["power.undervoltage_events"]; got != float64(s.events) {
			t.Errorf("at %s: power.undervoltage_events = %v, want %d", s.at, got, s.events)
		}
		if got := res.Series["power.undervoltage_events_5m"]; got != float64(s.recent) {
			t.Errorf("at %s: power.undervoltage_events_5m = %v, want %d", s.at, got, s.recent)
		}
		d := res.Data.(throttleData)
		if d.UndervoltageEvents == nil || *d.UndervoltageEvents != s.events {
			t.Errorf("at %s: UndervoltageEvents = %v, want %d", s.at, deref(d.UndervoltageEvents), s.events)
		}
		switch {
		case s.events == 0 && d.LastUndervoltage != nil:
			t.Errorf("at %s: LastUndervoltage = %v, want none", s.at, *d.LastUndervoltage)
		case s.events > 0 && (d.LastUndervoltage == nil || !d.LastUndervoltage.Equal(start.Add(s.last))):
			t.Errorf("at %s: LastUndervoltage = %v, want %v", s.at, deref(d.LastUndervoltage), start.Add(s.last))
		}
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
