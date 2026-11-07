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

func TestTemperature(t *testing.T) {
	// A Pi 5 with the active cooler and an NVMe drive on the PCIe connector.
	// hwmon0 is the thermal subsystem's copy of thermal_zone0 and must not
	// be reported twice; hwmon2 (voltages) and hwmon3 (fan) have no temps.
	env := &metrics.Env{FS: os.DirFS("testdata/temperature/pi5")}

	res, err := temperature{}.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	wantSeries := map[string]float64{
		"temp.cpu_c":           81, // 80950 m°C, rounded to one decimal
		"temp.rp1_c":           65.9,
		"temp.nvme_c":          44.9,
		"temp.nvme_sensor_1_c": 44.9,
		"temp.nvme_sensor_2_c": 51.9,
	}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}

	data, ok := res.Data.(temperatureData)
	if !ok {
		t.Fatalf("Data is %T, want temperatureData", res.Data)
	}
	want := temperatureData{Sensors: []temperatureSensor{
		{
			Name: "cpu", Source: "thermal_zone0", Type: "cpu-thermal", TempC: 81,
			Trips: []temperatureTrip{
				{Type: "critical", TempC: 110},
				{Type: "active", TempC: 50},
				{Type: "active", TempC: 60},
				{Type: "active", TempC: 67.5},
				{Type: "active", TempC: 75},
			},
		},
		{Name: "rp1", Source: "hwmon1/temp1", Type: "rp1_adc", TempC: 65.9},
		{Name: "nvme", Source: "hwmon4/temp1", Type: "nvme", Label: "Composite", TempC: 44.9},
		{Name: "nvme_sensor_1", Source: "hwmon4/temp2", Type: "nvme", Label: "Sensor 1", TempC: 44.9},
		{Name: "nvme_sensor_2", Source: "hwmon4/temp3", Type: "nvme", Label: "Sensor 2", TempC: 51.9},
	}}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Data = %+v, want %+v", data, want)
	}
}

func TestTemperatureEmpty(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"no sysfs": {},
		"no temperature sensors": {
			"sys/class/thermal/cooling_device0/type": {Data: []byte("pwm-fan\n")},
			"sys/class/hwmon/hwmon0/name":            {Data: []byte("rpi_volt\n")},
			"sys/class/hwmon/hwmon0/in0_lcrit_alarm": {Data: []byte("0\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			res, err := temperature{}.Collect(context.Background(), &metrics.Env{FS: fsys})
			if err != nil {
				t.Fatalf("Collect() error = %v, want an empty section", err)
			}
			if len(res.Series) != 0 {
				t.Errorf("Series = %v, want none", res.Series)
			}
			b, err := json.Marshal(res.Data)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(b); got != `{"sensors":[]}` {
				t.Errorf("Data = %s, want {\"sensors\":[]}", got)
			}
		})
	}
}

func TestTemperatureNamesAndDuplicates(t *testing.T) {
	const tz, hw = "sys/class/thermal/", "sys/class/hwmon/"
	val := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s + "\n")} }
	fsys := fstest.MapFS{
		// Two zones of the same type share one thermal_hwmon chip (hwmon0),
		// which is not linked from the zone directories here, so it must be
		// recognised by its name.
		tz + "thermal_zone0/type":       val("acpitz"),
		tz + "thermal_zone0/temp":       val("27800"),
		tz + "thermal_zone1/type":       val("acpitz"),
		tz + "thermal_zone1/temp":       val("-5049"),
		tz + "thermal_zone2/type":       val(""),
		tz + "thermal_zone2/temp":       val("40000"),
		tz + "thermal_zone3/type":       val("gpu-thermal"),
		tz + "thermal_zone3/temp/x":     val("a directory, so unreadable"),
		tz + "thermal_zone10/type":      val("soc_thermal"),
		tz + "thermal_zone10/temp":      val("50001"),
		hw + "hwmon0/name":              val("acpitz"),
		hw + "hwmon0/temp1_input":       val("27800"),
		hw + "hwmon0/temp2_input":       val("-5049"),
		hw + "hwmon1/name":              val("nvme"),
		hw + "hwmon1/temp1_input":       val("38850"),
		hw + "hwmon1/temp1_label":       val("Composite"),
		hw + "hwmon1/temp2_input":       val("41850"),
		hw + "hwmon1/temp2_label":       val("Sensor 1"),
		hw + "hwmon2/name":              val("nvme"),
		hw + "hwmon2/temp1_input":       val("36850"),
		hw + "hwmon2/temp1_label":       val("Composite"),
		hw + "hwmon2/temp2_input":       val("39850"),
		hw + "hwmon2/temp2_label":       val("Sensor 1"),
		hw + "hwmon3/name":              val("k10temp"),
		hw + "hwmon3/temp1_input/x":     val("unreadable"),
		hw + "hwmon3/temp3_input":       val("61125"),
		hw + "hwmon3/temp4_input":       val("58000"),
		hw + "hwmon12/name":             val("Weird Chip!"),
		hw + "hwmon12/temp1_input":      val("30000"),
		hw + "hwmon13/temp1_input":      val("31000"),
		hw + "hwmon13/temp1_label":      val("--"),
		hw + "hwmon13/temp2_input":      val("32000"),
		hw + "hwmon13/temp2_label":      val("--"),
		hw + "hwmon13/temp10_input":     val("33000"),
		hw + "hwmon13/temp10_crit":      val("99000"),
		hw + "hwmon13/temp10_crit_hyst": val("98000"),
	}

	res, err := temperature{}.Collect(context.Background(), &metrics.Env{FS: fsys})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	want := map[string]float64{
		"temp.acpitz_c":          27.8,
		"temp.acpitz_2_c":        -5,
		"temp.thermal_zone2_c":   40,
		"temp.soc_c":             50,
		"temp.nvme_c":            38.9,
		"temp.nvme_sensor_1_c":   41.9,
		"temp.nvme_2_c":          36.9,
		"temp.nvme_2_sensor_1_c": 39.9,
		"temp.k10temp_temp3_c":   61.1, // temp1 is unreadable; names stay put
		"temp.k10temp_temp4_c":   58,
		"temp.weird_chip_c":      30,
		"temp.hwmon13_c":         31,
		"temp.hwmon13_temp2_c":   32,
		"temp.hwmon13_temp10_c":  33,
	}
	if !reflect.DeepEqual(res.Series, want) {
		t.Errorf("Series = %v\nwant %v", res.Series, want)
	}

	data := res.Data.(temperatureData)
	var order []string
	for _, s := range data.Sensors {
		order = append(order, s.Source)
	}
	wantOrder := []string{
		"thermal_zone0", "thermal_zone1", "thermal_zone2", "thermal_zone10",
		"hwmon1/temp1", "hwmon1/temp2", "hwmon2/temp1", "hwmon2/temp2",
		"hwmon3/temp3", "hwmon3/temp4", "hwmon12/temp1",
		"hwmon13/temp1", "hwmon13/temp2", "hwmon13/temp10",
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("sensor order = %v\nwant %v", order, wantOrder)
	}
}

func TestTemperatureRounding(t *testing.T) {
	tests := map[string]float64{
		"80950":   81,
		"65931":   65.9,
		"44849":   44.8,
		"0":       0,
		"-273150": -273.2,
		"-50":     -0.1,
		"123":     0.1,
	}
	for in, want := range tests {
		fsys := fstest.MapFS{"sys/class/thermal/thermal_zone0/temp": {Data: []byte(in + "\n")}}
		res, err := temperature{}.Collect(context.Background(), &metrics.Env{FS: fsys})
		if err != nil {
			t.Fatalf("%s: Collect() error = %v", in, err)
		}
		if got := res.Series["temp.thermal_zone0_c"]; got != want {
			t.Errorf("%s m°C = %v °C, want %v", in, got, want)
		}
	}
}

func TestTemperatureErrors(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"garbage zone temp": {
			"sys/class/thermal/thermal_zone0/temp": {Data: []byte("hot\n")},
		},
		"garbage trip temp": {
			"sys/class/thermal/thermal_zone0/temp":              {Data: []byte("45000\n")},
			"sys/class/thermal/thermal_zone0/trip_point_0_temp": {Data: []byte("1e5\n")},
		},
		"garbage hwmon temp": {
			"sys/class/hwmon/hwmon0/name":        {Data: []byte("nvme\n")},
			"sys/class/hwmon/hwmon0/temp1_input": {Data: []byte("\n")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := (temperature{}).Collect(context.Background(), &metrics.Env{FS: fsys}); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestTemperatureIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "temperature" {
			return
		}
	}
	t.Fatal("temperature collector is not registered")
}
