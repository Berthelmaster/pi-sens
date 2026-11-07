package collectors

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(temperature{}) }

// temperature reports every temperature sensor the kernel exposes: the thermal
// zones under /sys/class/thermal (with their trip points) and the temp*_input
// channels of the hwmon chips under /sys/class/hwmon, which is where NVMe
// drives and the Pi 5's RP1 show up.
//
// Each sensor gets a short, stable name used in its series key,
// temp.<name>_c. The SoC zone (cpu-thermal) becomes "cpu", so temp.cpu_c is
// always there on a Pi.
//
// The thermal subsystem also registers a hwmon chip for each zone type
// (cpu_thermal on a Pi), so the same sensor would show up twice. Those chips
// are skipped; the zone is reported instead because it has the trip points.
//
// Sensors whose temperature cannot be read right now (a drive in deep sleep,
// a zone still probing) are left out rather than failing the whole section.
// A missing /sys/class/thermal or /sys/class/hwmon gives an empty section.
type temperature struct{}

const (
	tempThermalDir = "sys/class/thermal"
	tempHwmonDir   = "sys/class/hwmon"
)

type temperatureData struct {
	Sensors []temperatureSensor `json:"sensors"`
}

type temperatureSensor struct {
	// Name is the key used in the series, temp.<name>_c: "cpu", "nvme".
	Name string `json:"name"`
	// Source is where the reading comes from: "thermal_zone0" or
	// "hwmon3/temp1".
	Source string `json:"source"`
	// Type is the zone type ("cpu-thermal") or hwmon chip name ("nvme").
	Type string `json:"type"`
	// Label is the hwmon channel label, e.g. "Composite"; empty for zones.
	Label string  `json:"label,omitempty"`
	TempC float64 `json:"temp_c"`
	// Trips lists a thermal zone's trip points in kernel order. On a Pi the
	// active trips are the fan speed steps.
	Trips []temperatureTrip `json:"trips,omitempty"`
}

type temperatureTrip struct {
	Type  string  `json:"type"` // critical, hot, passive or active
	TempC float64 `json:"temp_c"`
}

// temperatureAliases gives nicer names to well-known sensors, keyed by the
// sanitised zone type or chip name after any "_thermal" suffix is dropped.
var temperatureAliases = map[string]string{
	"rp1_adc": "rp1", // Pi 5 southbridge
}

func (temperature) Name() string { return "temperature" }

func (temperature) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	names := temperatureNames{}

	zones, err := tempReadZones(env.FS, names)
	if err != nil {
		return metrics.Result{}, err
	}
	chips, err := tempReadHwmon(env.FS, names, zones.skipHwmon)
	if err != nil {
		return metrics.Result{}, err
	}

	data := temperatureData{Sensors: append(zones.sensors, chips...)}
	if data.Sensors == nil {
		data.Sensors = []temperatureSensor{}
	}
	if len(data.Sensors) == 0 {
		return metrics.Result{Data: data}, nil
	}
	series := make(map[string]float64, len(data.Sensors))
	for _, s := range data.Sensors {
		series["temp."+s.Name+"_c"] = s.TempC
	}
	return metrics.Result{Data: data, Series: series}, nil
}

type tempZones struct {
	sensors []temperatureSensor
	// skipHwmon holds the hwmon<N> directories and chip names the thermal
	// subsystem created for the zones, which duplicate them.
	skipHwmon map[string]bool
}

func tempReadZones(fsys fs.FS, names temperatureNames) (tempZones, error) {
	zones := tempZones{skipHwmon: map[string]bool{}}
	nums, err := tempNumberedEntries(fsys, tempThermalDir, "thermal_zone")
	if err != nil {
		return zones, err
	}
	for _, n := range nums {
		id := "thermal_zone" + strconv.Itoa(n)
		dir := path.Join(tempThermalDir, id)

		temp, ok, err := tempReadMillidegrees(fsys, path.Join(dir, "temp"))
		if err != nil {
			return zones, err
		}
		if !ok {
			continue
		}
		typ, _ := tempReadValue(fsys, path.Join(dir, "type"))
		trips, err := tempReadTrips(fsys, dir)
		if err != nil {
			return zones, err
		}

		// thermal_hwmon names its chip after the zone type with '-' turned
		// into '_', and links it as a hwmon<N> directory inside the zone.
		if typ != "" {
			zones.skipHwmon["name:"+strings.ReplaceAll(typ, "-", "_")] = true
		}
		if hw, err := tempNumberedEntries(fsys, dir, "hwmon"); err == nil {
			for _, h := range hw {
				zones.skipHwmon["hwmon"+strconv.Itoa(h)] = true
			}
		}

		zones.sensors = append(zones.sensors, temperatureSensor{
			Name:   names.claim(temperatureBaseName(typ, id)),
			Source: id,
			Type:   typ,
			TempC:  temp,
			Trips:  trips,
		})
	}
	return zones, nil
}

// tempReadTrips reads trip_point_<N>_temp and _type from a thermal zone directory.
// Trips whose temperature is missing or unreadable are skipped.
func tempReadTrips(fsys fs.FS, dir string) ([]temperatureTrip, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, nil // the temp file was readable; just no trips
	}
	var nums []int
	for _, e := range entries {
		s, ok := strings.CutPrefix(e.Name(), "trip_point_")
		if !ok {
			continue
		}
		s, ok = strings.CutSuffix(s, "_temp")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)

	var trips []temperatureTrip
	for _, n := range nums {
		prefix := path.Join(dir, "trip_point_"+strconv.Itoa(n))
		temp, ok, err := tempReadMillidegrees(fsys, prefix+"_temp")
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		typ, _ := tempReadValue(fsys, prefix+"_type")
		trips = append(trips, temperatureTrip{Type: typ, TempC: temp})
	}
	return trips, nil
}

// tempReadHwmon reads the temp<N>_input channels of every hwmon chip except
// those in skip, which holds "hwmon<N>" directory names and "name:<chip>"
// entries.
func tempReadHwmon(fsys fs.FS, names temperatureNames, skip map[string]bool) ([]temperatureSensor, error) {
	nums, err := tempNumberedEntries(fsys, tempHwmonDir, "hwmon")
	if err != nil {
		return nil, err
	}
	var sensors []temperatureSensor
	for _, n := range nums {
		id := "hwmon" + strconv.Itoa(n)
		dir := path.Join(tempHwmonDir, id)
		chip, _ := tempReadValue(fsys, path.Join(dir, "name"))
		if skip[id] || (chip != "" && skip["name:"+chip]) {
			continue
		}

		channels := tempHwmonChannels(fsys, dir)
		var base string // claimed lazily so chips without temps take no name
		for _, ch := range channels {
			chID := "temp" + strconv.Itoa(ch)
			temp, ok, err := tempReadMillidegrees(fsys, path.Join(dir, chID+"_input"))
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			label, _ := tempReadValue(fsys, path.Join(dir, chID+"_label"))
			if base == "" {
				base = names.claim(temperatureBaseName(chip, id))
			}

			// The chip's first channel (NVMe "Composite", the only one on
			// most chips) gets the chip's name, the rest are qualified.
			name := base
			if ch != channels[0] {
				suffix := tempSanitize(label)
				if suffix == "" {
					suffix = chID
				}
				name = names.claim(base + "_" + suffix)
			}
			sensors = append(sensors, temperatureSensor{
				Name:   name,
				Source: id + "/" + chID,
				Type:   chip,
				Label:  label,
				TempC:  temp,
			})
		}
	}
	return sensors, nil
}

// tempHwmonChannels returns the N of every temp<N>_input in dir, ascending.
// An unreadable dir has none.
func tempHwmonChannels(fsys fs.FS, dir string) []int {
	entries, _ := fs.ReadDir(fsys, dir)
	var chans []int
	for _, e := range entries {
		s, ok := strings.CutPrefix(e.Name(), "temp")
		if !ok {
			continue
		}
		s, ok = strings.CutSuffix(s, "_input")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			chans = append(chans, n)
		}
	}
	sort.Ints(chans)
	return chans
}

// tempNumberedEntries returns N for every <prefix><N> entry of dir, ascending. A
// missing dir yields none.
func tempNumberedEntries(fsys fs.FS, dir, prefix string) ([]int, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var nums []int
	for _, e := range entries {
		s, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	return nums, nil
}

// tempReadMillidegrees reads a sysfs temperature in m°C and returns it in °C
// rounded to one decimal. ok is false if the file is missing or cannot be
// read; sysfs returns errors such as EIO or ENODATA for sensors that are
// asleep or not ready. A value that is not a number is an error.
func tempReadMillidegrees(fsys fs.FS, name string) (c float64, ok bool, err error) {
	s, ok := tempReadValue(fsys, name)
	if !ok {
		return 0, false, nil
	}
	m, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	return math.Round(float64(m)/100) / 10, true, nil
}

// tempReadValue reads a single-value sysfs file. ok is false if it is
// missing or cannot be read.
func tempReadValue(fsys fs.FS, name string) (string, bool) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// temperatureBaseName turns a zone type or chip name into a series name:
// "cpu-thermal" → "cpu", "rp1_adc" → "rp1", "nvme" → "nvme". fallback is
// used when nothing is left after sanitising.
func temperatureBaseName(raw, fallback string) string {
	name := tempSanitize(raw)
	if trimmed := strings.TrimSuffix(name, "_thermal"); trimmed != "" {
		name = trimmed
	}
	if alias, ok := temperatureAliases[name]; ok {
		name = alias
	}
	if name == "" {
		return fallback
	}
	return name
}

// tempSanitize lowercases s and replaces every run of characters other
// than a-z and 0-9 with a single underscore: "Sensor 1" → "sensor_1".
func tempSanitize(s string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pending && b.Len() > 0 {
				b.WriteByte('_')
			}
			pending = false
			b.WriteRune(r)
		} else {
			pending = true
		}
	}
	return b.String()
}

// temperatureNames hands out unique sensor names, appending _2, _3, ... when
// a name is taken (two NVMe drives become nvme and nvme_2).
type temperatureNames map[string]bool

func (t temperatureNames) claim(name string) string {
	unique := name
	for i := 2; t[unique]; i++ {
		unique = name + "_" + strconv.Itoa(i)
	}
	t[unique] = true
	return unique
}
