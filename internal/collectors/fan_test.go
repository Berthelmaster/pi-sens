package collectors

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func TestFan(t *testing.T) {
	// A Pi 5 with the active cooler at fan level 2 of 4 (PWM 125 of 255).
	env := &metrics.Env{FS: os.DirFS("testdata/fan/pi5")}

	res, err := fan{}.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	wantSeries := map[string]float64{"fan.rpm": 2953, "fan.pwm_pct": 49}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}

	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"present":true,"hwmon":"hwmon3","chip":"pwmfan","rpm":2953,"pwm":125,"pwm_pct":49,` +
		`"cooling_device":"cooling_device0","level":2,"max_level":4}`
	if got := string(b); got != want {
		t.Errorf("Data = %s\nwant %s", got, want)
	}
}

func TestFanAbsent(t *testing.T) {
	tests := map[string]*metrics.Env{
		// A Pi 5 without the cooler: other hwmon chips, no fan.
		"pi5 without cooler": {FS: os.DirFS("testdata/fan/absent")},
		"no sysfs":           {FS: fstest.MapFS{}},
		"other cooling device only": {FS: fstest.MapFS{
			"sys/class/thermal/cooling_device0/type":      {Data: []byte("Processor\n")},
			"sys/class/thermal/cooling_device0/cur_state": {Data: []byte("0\n")},
			"sys/class/thermal/cooling_device0/max_state": {Data: []byte("3\n")},
		}},
	}
	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			res, err := fan{}.Collect(context.Background(), env)
			if err != nil {
				t.Fatalf("Collect() error = %v, want present: false", err)
			}
			if len(res.Series) != 0 {
				t.Errorf("Series = %v, want none", res.Series)
			}
			b, err := json.Marshal(res.Data)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(b); got != `{"present":false}` {
				t.Errorf("Data = %s, want {\"present\":false}", got)
			}
		})
	}
}

func TestFanDiscovery(t *testing.T) {
	const plat, hw, th = "sys/devices/platform/cooling_fan/hwmon/", "sys/class/hwmon/", "sys/class/thermal/"
	val := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s + "\n")} }

	tests := []struct {
		name       string
		fsys       fstest.MapFS
		wantHwmon  string
		wantCdev   string
		wantSeries map[string]float64
	}{
		{
			// Another boot of the same Pi 5: the chip came up as hwmon5, and
			// the pwm-fan cooling device is not the first one.
			name: "renumbered after reboot",
			fsys: fstest.MapFS{
				plat + "hwmon5/name":                  val("pwmfan"),
				plat + "hwmon5/fan1_input":            val("0"),
				plat + "hwmon5/pwm1":                  val("0"),
				hw + "hwmon0/name":                    val("cpu_thermal"),
				th + "cooling_device0/type":           val("thermal-cpufreq-0"),
				th + "cooling_device0/cur_state":      val("1"),
				th + "cooling_device0/max_state":      val("7"),
				th + "cooling_device12/type":          val("pwm-fan"),
				th + "cooling_device12/cur_state":     val("0"),
				th + "cooling_device12/max_state":     val("4"),
				th + "cooling_device12/stats/trans_x": val("ignored"),
			},
			wantHwmon:  "hwmon5",
			wantCdev:   "cooling_device12",
			wantSeries: map[string]float64{"fan.rpm": 0, "fan.pwm_pct": 0},
		},
		{
			// The platform device wins over another chip with a fan.
			name: "platform device first",
			fsys: fstest.MapFS{
				plat + "hwmon7/name":       val("pwmfan"),
				plat + "hwmon7/fan1_input": val("4980"),
				plat + "hwmon7/pwm1":       val("255"),
				hw + "hwmon2/name":         val("nct6775"),
				hw + "hwmon2/fan1_input":   val("812"),
				hw + "hwmon7/name":         val("pwmfan"),
				hw + "hwmon7/fan1_input":   val("4980"),
			},
			wantHwmon:  "hwmon7",
			wantSeries: map[string]float64{"fan.rpm": 4980, "fan.pwm_pct": 100},
		},
		{
			// No cooling_fan device: the first chip with fan1_input, which
			// here has no pwm1.
			name: "any hwmon with a fan",
			fsys: fstest.MapFS{
				hw + "hwmon0/name":        val("acpitz"),
				hw + "hwmon0/temp1_input": val("27800"),
				hw + "hwmon3/name":        val("thinkpad"),
				hw + "hwmon3/fan1_input":  val("2410"),
				hw + "hwmon11/name":       val("other"),
				hw + "hwmon11/fan1_input": val("999"),
			},
			wantHwmon:  "hwmon3",
			wantSeries: map[string]float64{"fan.rpm": 2410},
		},
		{
			// The driver is bound but no hwmon chip yet: the level alone.
			name: "cooling device only",
			fsys: fstest.MapFS{
				th + "cooling_device0/type":      val("pwm-fan"),
				th + "cooling_device0/cur_state": val("3"),
				th + "cooling_device0/max_state": val("4"),
			},
			wantCdev: "cooling_device0",
		},
		{
			name: "pwm rounding",
			fsys: fstest.MapFS{
				plat + "hwmon3/fan1_input": val("1500"),
				plat + "hwmon3/pwm1":       val("75"),
			},
			wantHwmon:  "hwmon3",
			wantSeries: map[string]float64{"fan.rpm": 1500, "fan.pwm_pct": 29.4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := fan{}.Collect(context.Background(), &metrics.Env{FS: tt.fsys})
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			data := res.Data.(fanData)
			if !data.Present {
				t.Error("Present = false, want true")
			}
			if data.Hwmon != tt.wantHwmon || data.CoolingDevice != tt.wantCdev {
				t.Errorf("hwmon, cooling device = %q, %q, want %q, %q",
					data.Hwmon, data.CoolingDevice, tt.wantHwmon, tt.wantCdev)
			}
			if len(res.Series) != 0 || len(tt.wantSeries) != 0 {
				if !reflect.DeepEqual(res.Series, tt.wantSeries) {
					t.Errorf("Series = %v, want %v", res.Series, tt.wantSeries)
				}
			}
		})
	}
}

func TestFanErrors(t *testing.T) {
	const plat, th = "sys/devices/platform/cooling_fan/hwmon/hwmon3/", "sys/class/thermal/cooling_device0/"
	tests := map[string]fstest.MapFS{
		"garbage rpm": {
			plat + "fan1_input": {Data: []byte("fast\n")},
		},
		"garbage pwm": {
			plat + "fan1_input": {Data: []byte("2953\n")},
			plat + "pwm1":       {Data: []byte("\n")},
		},
		"pwm out of range": {
			plat + "fan1_input": {Data: []byte("2953\n")},
			plat + "pwm1":       {Data: []byte("256\n")},
		},
		"garbage cur_state": {
			th + "type":      {Data: []byte("pwm-fan\n")},
			th + "cur_state": {Data: []byte("high\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := (fan{}).Collect(context.Background(), &metrics.Env{FS: fsys}); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestFanIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "fan" {
			return
		}
	}
	t.Fatal("fan collector is not registered")
}
