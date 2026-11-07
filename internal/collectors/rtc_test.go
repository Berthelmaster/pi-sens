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

func TestRTC(t *testing.T) {
	tests := []struct {
		name       string
		env        *metrics.Env
		wantData   string
		wantSeries map[string]float64
	}{
		{
			// A Pi 5 with a battery fitted and trickle charging to 3.0 V.
			name: "battery",
			env:  &metrics.Env{FS: os.DirFS("testdata/rtc/battery")},
			wantData: `{"present":true,"rtc":"rtc0","name":"rpi-rtc soc@107c000000:rpi_rtc",` +
				`"battery":true,"battery_v":3.021,"charging":true,"charging_voltage_v":3}`,
			wantSeries: map[string]float64{"power.rtc_battery_v": 3.021},
		},
		{
			// A Pi 5 without a battery: the RTC reads 0 V.
			name:     "no battery",
			env:      &metrics.Env{FS: os.DirFS("testdata/rtc/no_battery")},
			wantData: `{"present":true,"rtc":"rtc0","name":"rpi-rtc soc@107c000000:rpi_rtc","battery":false}`,
		},
		{
			// A Pi 4: no RTC at all.
			name:     "no rtc",
			env:      &metrics.Env{FS: os.DirFS("testdata/rtc/no_rtc")},
			wantData: `{"present":false,"battery":false}`,
		},
		{
			name:     "no sysfs",
			env:      &metrics.Env{FS: fstest.MapFS{}},
			wantData: `{"present":false,"battery":false}`,
		},
		{
			// An RTC HAT that does not report its battery.
			name: "rtc without battery reading",
			env: &metrics.Env{FS: fstest.MapFS{
				"sys/class/rtc/rtc0/name": {Data: []byte("rtc-ds1307 1-0068\n")},
			}},
			wantData: `{"present":false,"battery":false}`,
		},
		{
			// A battery that is not being charged: charging is off and has no
			// voltage.
			name: "not charging",
			env: &metrics.Env{FS: fstest.MapFS{
				"sys/class/rtc/rtc0/battery_voltage":  {Data: []byte("2650000\n")},
				"sys/class/rtc/rtc0/charging_voltage": {Data: []byte("0\n")},
			}},
			wantData:   `{"present":true,"rtc":"rtc0","battery":true,"battery_v":2.65,"charging":false}`,
			wantSeries: map[string]float64{"power.rtc_battery_v": 2.65},
		},
		{
			// An RTC that cannot charge leaves charging out.
			name: "no charging support",
			env: &metrics.Env{FS: fstest.MapFS{
				"sys/class/rtc/rtc0/battery_voltage": {Data: []byte("3100000\n")},
			}},
			wantData:   `{"present":true,"rtc":"rtc0","battery":true,"battery_v":3.1}`,
			wantSeries: map[string]float64{"power.rtc_battery_v": 3.1},
		},
		{
			// Noise just above 0 V is still no battery.
			name: "near zero",
			env: &metrics.Env{FS: fstest.MapFS{
				"sys/class/rtc/rtc0/battery_voltage": {Data: []byte("12000\n")},
			}},
			wantData: `{"present":true,"rtc":"rtc0","battery":false}`,
		},
		{
			// rtc0 is a HAT without a battery reading, rtc2 the Pi 5 RTC;
			// rtc10 sorts after rtc2 and is never reached.
			name: "first rtc with a battery reading",
			env: &metrics.Env{FS: fstest.MapFS{
				"sys/class/rtc/rtc0/name":             {Data: []byte("rtc-ds1307 1-0068\n")},
				"sys/class/rtc/rtc2/name":             {Data: []byte("rpi-rtc\n")},
				"sys/class/rtc/rtc2/battery_voltage":  {Data: []byte("2950000\n")},
				"sys/class/rtc/rtc10/battery_voltage": {Data: []byte("not read\n")},
			}},
			wantData:   `{"present":true,"rtc":"rtc2","name":"rpi-rtc","battery":true,"battery_v":2.95}`,
			wantSeries: map[string]float64{"power.rtc_battery_v": 2.95},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := rtc{}.Collect(context.Background(), tt.env)
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if !reflect.DeepEqual(res.Series, tt.wantSeries) {
				t.Errorf("Series = %v, want %v", res.Series, tt.wantSeries)
			}
			b, err := json.Marshal(res.Data)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(b); got != tt.wantData {
				t.Errorf("Data = %s\nwant %s", got, tt.wantData)
			}
		})
	}
}

func TestRTCBadValues(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"battery_voltage": {
			"sys/class/rtc/rtc0/battery_voltage": {Data: []byte("3.0V\n")},
		},
		"charging_voltage": {
			"sys/class/rtc/rtc0/battery_voltage":  {Data: []byte("3000000\n")},
			"sys/class/rtc/rtc0/charging_voltage": {Data: []byte("on\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := (rtc{}).Collect(context.Background(), &metrics.Env{FS: fsys}); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestRTCIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "rtc" {
			return
		}
	}
	t.Fatal("rtc collector is not registered")
}
