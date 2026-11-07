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

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// powerResponses returns the measure_volts and measure_clock answers of a
// Pi 5 under some load, plus the given pmic_read_adc response.
func powerResponses(t *testing.T, pmic *metrics.FakeResponse) map[string]metrics.FakeResponse {
	t.Helper()
	r := map[string]metrics.FakeResponse{
		"vcgencmd measure_volts core":    {Output: "volt=0.7278V\n"},
		"vcgencmd measure_volts sdram_c": {Output: "volt=0.6000V\n"},
		"vcgencmd measure_volts sdram_i": {Output: "volt=0.6000V\n"},
		"vcgencmd measure_volts sdram_p": {Output: "volt=1.1000V\n"},
		"vcgencmd measure_clock arm":     {Output: "frequency(0)=2400033792\n"},
		"vcgencmd measure_clock core":    {Output: "frequency(0)=910007808\n"},
		"vcgencmd measure_clock v3d":     {Output: "frequency(0)=960012800\n"},
	}
	if pmic != nil {
		r["vcgencmd pmic_read_adc"] = *pmic
	}
	return r
}

func powerPi5PMIC(t *testing.T) *metrics.FakeResponse {
	t.Helper()
	b, err := os.ReadFile("testdata/power/pi5_pmic_read_adc.txt")
	if err != nil {
		t.Fatal(err)
	}
	return &metrics.FakeResponse{Output: string(b)}
}

func TestPowerPi5(t *testing.T) {
	runner := &metrics.FakeRunner{Responses: powerResponses(t, powerPi5PMIC(t))}
	res, err := (&power{}).Collect(context.Background(), &metrics.Env{Runner: runner})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	data := res.Data.(powerData)
	if !data.Available {
		t.Fatal("Available = false, want true")
	}
	if want := (powerVolts{Core: 0.7278, SDRAMC: 0.6, SDRAMI: 0.6, SDRAMP: 1.1}); *data.Volts != want {
		t.Errorf("Volts = %+v, want %+v", *data.Volts, want)
	}
	if want := (powerClocks{Arm: 2400.033792, Core: 910.007808, V3D: 960.0128}); *data.ClocksMHz != want {
		t.Errorf("ClocksMHz = %+v, want %+v", *data.ClocksMHz, want)
	}

	pmic := data.PMIC
	if pmic == nil {
		t.Fatal("PMIC = nil, want the Pi 5 rails")
	}
	// Every *_A reading is paired with the *_V of the same rail. EXT5V_V and
	// BATT_V have no current, so they are not rails.
	wantRails := []powerRail{
		{Name: "0V8_AON", Volts: 0.7992674, Amps: 0.0051282, Watts: 0.0041},
		{Name: "0V8_SW", Volts: 0.801221, Amps: 0.3284301, Watts: 0.2631},
		{Name: "1V1_SYS", Volts: 1.105494, Amps: 0.200574, Watts: 0.2217},
		{Name: "1V8_SYS", Volts: 1.800732, Amps: 0.1903248, Watts: 0.3427},
		{Name: "3V3_ADC", Volts: 3.305861, Amps: 0.00042735, Watts: 0.0014},
		{Name: "3V3_DAC", Volts: 3.305861, Amps: 0.0004884, Watts: 0.0016},
		{Name: "3V3_SYS", Volts: 3.310438, Amps: 0.06636296, Watts: 0.2197},
		{Name: "3V7_WL_SW", Volts: 3.73552, Amps: 0.00878472, Watts: 0.0328},
		{Name: "DDR_VDD2", Volts: 1.110804, Amps: 0.02831058, Watts: 0.0314},
		{Name: "DDR_VDDQ", Volts: 0.6051282, Amps: 0, Watts: 0},
		{Name: "HDMI", Volts: 5.14652, Amps: 0.018315, Watts: 0.0943},
		{Name: "VDD_CORE", Volts: 0.7278388, Amps: 1.2753, Watts: 0.9282},
	}
	if !reflect.DeepEqual(pmic.Rails, wantRails) {
		t.Errorf("Rails =\n%+v\nwant\n%+v", pmic.Rails, wantRails)
	}
	if pmic.TotalW != 2.1412 {
		t.Errorf("TotalW = %v, want 2.1412", pmic.TotalW)
	}
	if pmic.Ext5VV == nil || *pmic.Ext5VV != 5.138278 {
		t.Errorf("Ext5VV = %v, want 5.138278", pmic.Ext5VV)
	}

	wantSeries := map[string]float64{
		"pi.core_v":        0.7278,
		"pi.arm_clock_mhz": 2400.033792,
		"pi.power_w":       2.1412,
		"pi.ext5v_v":       5.138278,
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}
}

func TestPowerJSON(t *testing.T) {
	pmic := &metrics.FakeResponse{Output: "" +
		"  VDD_CORE_A current(7)=2.00000000A\n" +
		"   1V8_SYS_A current(2)=0.50000000A\n" +
		"  VDD_CORE_V volt(15)=0.80000000V\n" +
		"   1V8_SYS_V volt(10)=1.80000000V\n" +
		"     EXT5V_V volt(24)=5.10000000V\n"}
	runner := &metrics.FakeRunner{Responses: powerResponses(t, pmic)}
	res, err := (&power{}).Collect(context.Background(), &metrics.Env{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"available":true,` +
		`"volts":{"core":0.7278,"sdram_c":0.6,"sdram_i":0.6,"sdram_p":1.1},` +
		`"clocks_mhz":{"arm":2400.033792,"core":910.007808,"v3d":960.0128},` +
		`"pmic":{"ext5v_v":5.1,"total_w":2.5,"rails":[` +
		`{"name":"1V8_SYS","volts":1.8,"amps":0.5,"watts":0.9},` +
		`{"name":"VDD_CORE","volts":0.8,"amps":2,"watts":1.6}]}}`
	if string(b) != want {
		t.Errorf("JSON = %s\nwant %s", b, want)
	}
}

func TestPowerWithoutPMIC(t *testing.T) {
	tests := map[string]metrics.FakeResponse{
		// What vcgencmd prints for a command the firmware does not know: a
		// non-zero exit status with the firmware's error on stdout.
		"command not registered": {
			Output: "vc_gencmd_read_response returned 2\nerror=2 error_msg=\"Command not registered\"\n",
			Err:    errors.New("vcgencmd: exit status 2"),
		},
		// Older firmware that answers with the error but exits 0.
		"error with exit 0": {Output: "error=2 error_msg=\"Command not registered\"\n"},
		"no rails":          {Output: "\n"},
	}
	for name, resp := range tests {
		t.Run(name, func(t *testing.T) {
			runner := &metrics.FakeRunner{Responses: powerResponses(t, &resp)}
			p := &power{}
			env := &metrics.Env{Runner: runner}
			for i := 0; i < 2; i++ {
				res, err := p.Collect(context.Background(), env)
				if err != nil {
					t.Fatalf("Collect() #%d error = %v", i+1, err)
				}
				data := res.Data.(powerData)
				if !data.Available || data.Volts == nil || data.ClocksMHz == nil {
					t.Errorf("Data = %+v, want volts and clocks", data)
				}
				if data.PMIC != nil {
					t.Errorf("PMIC = %+v, want none", data.PMIC)
				}
				b, _ := json.Marshal(res.Data)
				if strings.Contains(string(b), "pmic") {
					t.Errorf("JSON = %s, want no pmic part", b)
				}
				wantSeries := map[string]float64{"pi.core_v": 0.7278, "pi.arm_clock_mhz": 2400.033792}
				if !reflect.DeepEqual(res.Series, wantSeries) {
					t.Errorf("Series = %v, want %v", res.Series, wantSeries)
				}
			}
			// The firmware does not grow the command; it is asked once.
			if n := powerCount(runner.Calls(), "vcgencmd pmic_read_adc"); n != 1 {
				t.Errorf("ran pmic_read_adc %d times, want 1", n)
			}
		})
	}
}

func TestPowerPMICTransientFailure(t *testing.T) {
	// A failure that is not the firmware rejecting the command leaves the
	// pmic part out this time but asks again next time.
	responses := powerResponses(t, &metrics.FakeResponse{Err: errors.New("vcgencmd: exit status 1")})
	runner := &metrics.FakeRunner{Responses: responses}
	p := &power{}
	env := &metrics.Env{Runner: runner}

	res, err := p.Collect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.(powerData).PMIC != nil {
		t.Error("PMIC present after a failed pmic_read_adc")
	}

	runner.Responses["vcgencmd pmic_read_adc"] = *powerPi5PMIC(t)
	res, err = p.Collect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.(powerData).PMIC == nil {
		t.Error("PMIC = nil after pmic_read_adc recovered")
	}
	if _, ok := res.Series["pi.power_w"]; !ok {
		t.Errorf("Series = %v, want pi.power_w", res.Series)
	}
}

func TestPowerRunsConcurrently(t *testing.T) {
	// Every call blocks until all eight have started, so a sequential
	// collector would never finish.
	r := &powerBarrierRunner{
		fake:    metrics.FakeRunner{Responses: powerResponses(t, powerPi5PMIC(t))},
		started: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
	go func() {
		for i := 0; i < 8; i++ {
			<-r.started
		}
		close(r.release)
	}()
	if _, err := (&power{}).Collect(context.Background(), &metrics.Env{Runner: r}); err != nil {
		t.Fatal(err)
	}
}

type powerBarrierRunner struct {
	fake    metrics.FakeRunner
	started chan struct{}
	release chan struct{}
}

func (r *powerBarrierRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.started <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.fake.Run(ctx, name, args...)
}

func TestPowerUnavailable(t *testing.T) {
	logs := captureLog(t)
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{}}
	missing := metrics.FakeResponse{Err: fmt.Errorf("vcgencmd: %w", &exec.Error{Name: "vcgencmd", Err: exec.ErrNotFound})}
	for line := range powerResponses(t, &missing) {
		runner.Responses[line] = missing
	}
	env := &metrics.Env{Runner: runner}

	for i := 0; i < 2; i++ {
		res, err := (&power{}).Collect(context.Background(), env)
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
	// Only the first, concurrent round runs vcgencmd at all.
	if n := len(runner.Calls()); n > 8 {
		t.Errorf("ran vcgencmd %d times, want at most 8", n)
	}
}

func TestPowerErrors(t *testing.T) {
	tests := map[string]struct {
		line string
		resp metrics.FakeResponse
	}{
		"volts not a number":  {"vcgencmd measure_volts core", metrics.FakeResponse{Output: "volt=abcV\n"}},
		"volts without unit":  {"vcgencmd measure_volts sdram_p", metrics.FakeResponse{Output: "volt=1.1\n"}},
		"clock garbage":       {"vcgencmd measure_clock arm", metrics.FakeResponse{Output: "frequency=fast\n"}},
		"clock unknown":       {"vcgencmd measure_clock v3d", metrics.FakeResponse{Output: "error=1 error_msg=\"Invalid arguments\"\n"}},
		"volts command fails": {"vcgencmd measure_volts core", metrics.FakeResponse{Err: errors.New("vcgencmd: exit status 1")}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			responses := powerResponses(t, powerPi5PMIC(t))
			responses[tt.line] = tt.resp
			runner := &metrics.FakeRunner{Responses: responses}
			_, err := (&power{}).Collect(context.Background(), &metrics.Env{Runner: runner})
			if err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestPowerCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &metrics.FakeRunner{Responses: powerResponses(t, powerPi5PMIC(t))}
	p := &power{}
	env := &metrics.Env{Runner: runner}
	if _, err := p.Collect(ctx, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
	// The cancellation must not be mistaken for a missing vcgencmd or PMIC.
	res, err := p.Collect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data.(powerData).PMIC == nil {
		t.Error("PMIC = nil after a canceled call, want the rails")
	}
}

func TestPowerIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "power" {
			return
		}
	}
	t.Fatal("power collector is not registered")
}

func TestVcgencmdErrorIncludesOutput(t *testing.T) {
	runner := &metrics.FakeRunner{Responses: map[string]metrics.FakeResponse{
		"vcgencmd foo": {
			Output: "vc_gencmd_read_response returned 2\nerror=2 error_msg=\"Command not registered\"\n",
			Err:    errors.New("vcgencmd: exit status 2"),
		},
	}}
	_, err := vcgencmd(context.Background(), &metrics.Env{Runner: runner}, "foo")
	if err == nil || !strings.Contains(err.Error(), `error_msg="Command not registered"`) {
		t.Fatalf("vcgencmd() error = %v, want it to include the firmware's message", err)
	}
}

func powerCount(calls []string, line string) int {
	n := 0
	for _, c := range calls {
		if c == line {
			n++
		}
	}
	return n
}
