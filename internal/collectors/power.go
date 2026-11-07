package collectors

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&power{}) }

// power reports the firmware's voltage and clock readings from
// "vcgencmd measure_volts" and "vcgencmd measure_clock", and on a Pi 5 the
// PMIC's per-rail currents and voltages from "vcgencmd pmic_read_adc", from
// which it works out the power drawn by each rail and in total.
//
// The vcgencmd calls are run concurrently, so a sample takes about as long as
// the slowest one rather than the sum of all of them.
//
// Each PMIC total is also handed to the energy meter (see SetEnergy), which
// adds up the kWh the energy collector reports.
//
// Without a usable vcgencmd the section is {"available": false} with no
// series. On a Pi without pmic_read_adc (anything before the Pi 5) the pmic
// part is left out.
type power struct {
	// pmicUnsupported is set once the firmware has said it does not know
	// pmic_read_adc, so it is not asked again on every sample.
	pmicUnsupported bool
}

// powerVoltIDs and powerClockIDs are the measure_volts and measure_clock
// arguments, in the order of the fields of powerVolts and powerClocks.
var (
	powerVoltIDs  = []string{"core", "sdram_c", "sdram_i", "sdram_p"}
	powerClockIDs = []string{"arm", "core", "v3d"}
)

// powerExt5V is the PMIC reading of the 5 V input supply.
const powerExt5V = "EXT5V"

type powerData struct {
	Available bool         `json:"available"`
	Volts     *powerVolts  `json:"volts,omitempty"`
	ClocksMHz *powerClocks `json:"clocks_mhz,omitempty"`
	PMIC      *powerPMIC   `json:"pmic,omitempty"`
}

type powerVolts struct {
	Core   float64 `json:"core"`
	SDRAMC float64 `json:"sdram_c"`
	SDRAMI float64 `json:"sdram_i"`
	SDRAMP float64 `json:"sdram_p"`
}

type powerClocks struct {
	Arm  float64 `json:"arm"`
	Core float64 `json:"core"`
	V3D  float64 `json:"v3d"`
}

type powerPMIC struct {
	// Ext5VV is the input supply voltage, omitted if the PMIC does not
	// report it.
	Ext5VV *float64 `json:"ext5v_v,omitempty"`
	// TotalW is the sum of the rails' power. It covers what the PMIC
	// supplies (SoC, RAM, wireless, HDMI and so on), not USB devices or the
	// losses in the PMIC itself, so it is an estimate that stays below what
	// the board draws from the supply.
	TotalW float64     `json:"total_w"`
	Rails  []powerRail `json:"rails"`
}

// powerRail is one PMIC rail with both a current and a voltage reading.
type powerRail struct {
	Name  string  `json:"name"`
	Volts float64 `json:"volts"`
	Amps  float64 `json:"amps"`
	Watts float64 `json:"watts"`
}

func (*power) Name() string { return "power" }

func (p *power) Collect(ctx context.Context, env *metrics.Env) (metrics.Result, error) {
	type call struct {
		args []string
		out  string
		err  error
	}
	var calls []*call
	for _, id := range powerVoltIDs {
		calls = append(calls, &call{args: []string{"measure_volts", id}})
	}
	for _, id := range powerClockIDs {
		calls = append(calls, &call{args: []string{"measure_clock", id}})
	}
	var pmic *call
	if !p.pmicUnsupported {
		pmic = &call{args: []string{"pmic_read_adc"}}
		calls = append(calls, pmic)
	}

	var wg sync.WaitGroup
	for _, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.out, c.err = vcgencmd(ctx, env, c.args...)
		}()
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return metrics.Result{}, err
	}
	for _, c := range calls {
		if errors.Is(c.err, errVcgencmdUnavailable) {
			return metrics.Result{Data: powerData{Available: false}}, nil
		}
	}

	// The volts and clocks come first in calls, in the order of the IDs.
	values := make([]float64, len(powerVoltIDs)+len(powerClockIDs))
	for i := range values {
		c := calls[i]
		if c.err != nil {
			return metrics.Result{}, c.err
		}
		var err error
		if i < len(powerVoltIDs) {
			values[i], err = powerParseVolts(c.out)
		} else {
			values[i], err = powerParseClockMHz(c.out)
		}
		if err != nil {
			return metrics.Result{}, fmt.Errorf("vcgencmd %s: %w", strings.Join(c.args, " "), err)
		}
	}
	volts := powerVolts{Core: values[0], SDRAMC: values[1], SDRAMI: values[2], SDRAMP: values[3]}
	clocks := powerClocks{Arm: values[4], Core: values[5], V3D: values[6]}

	data := powerData{Available: true, Volts: &volts, ClocksMHz: &clocks}
	series := map[string]float64{
		"pi.core_v":        volts.Core,
		"pi.arm_clock_mhz": clocks.Arm,
	}

	if pmic != nil {
		// A failed PMIC reading leaves the pmic part out rather than failing
		// the voltages and clocks along with it. Only a firmware that says it
		// does not know the command (or answers with no rails) stops it
		// from being asked again; anything else is retried next sample.
		switch {
		case pmic.err != nil:
			p.pmicUnsupported = powerFirmwareError(pmic.err.Error())
		case powerFirmwareError(pmic.out):
			p.pmicUnsupported = true
		default:
			d := powerParsePMIC(pmic.out)
			if d == nil {
				p.pmicUnsupported = true
				break
			}
			data.PMIC = d
			series["pi.power_w"] = d.TotalW
			energyAdd(env.Now, d.TotalW)
			if d.Ext5VV != nil {
				series["pi.ext5v_v"] = *d.Ext5VV
			}
		}
	}

	return metrics.Result{Data: data, Series: series}, nil
}

// powerFirmwareError reports whether vcgencmd output (or the error carrying
// it) is the firmware rejecting the command: error=2 error_msg="Command not
// registered".
func powerFirmwareError(s string) bool {
	return strings.Contains(s, "error=")
}

// powerParseVolts reads the output of measure_volts, "volt=0.7200V".
func powerParseVolts(out string) (float64, error) {
	s, ok := strings.CutPrefix(strings.TrimSpace(out), "volt=")
	if s, ok2 := strings.CutSuffix(s, "V"); ok && ok2 {
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v, nil
		}
	}
	return 0, fmt.Errorf("unexpected output %q", out)
}

var powerClockRe = regexp.MustCompile(`^frequency\(\d+\)=(\d+)$`)

// powerParseClockMHz reads the output of measure_clock, "frequency(0)=1500000000"
// in Hz, and returns MHz.
func powerParseClockMHz(out string) (float64, error) {
	m := powerClockRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return 0, fmt.Errorf("unexpected output %q", out)
	}
	hz, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected output %q: %w", out, err)
	}
	return float64(hz) / 1e6, nil
}

// powerADCRe matches one line of pmic_read_adc:
//
//	VDD_CORE_A current(7)=2.68374000A
//	VDD_CORE_V volt(15)=0.78002580V
var powerADCRe = regexp.MustCompile(`^(\S+)_([AV])\s+(?:current|volt)\(\d+\)=([-+0-9.eE]+)[AV]$`)

// powerParsePMIC reads the output of pmic_read_adc, pairs each rail's current
// and voltage by name and works out its power. Readings without a partner,
// such as EXT5V_V and BATT_V, are not rails; EXT5V_V is kept as the input
// supply voltage. It returns nil if there are no rails at all.
func powerParsePMIC(out string) *powerPMIC {
	amps := map[string]float64{}
	volts := map[string]float64{}
	for _, line := range strings.Split(out, "\n") {
		m := powerADCRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		v, err := strconv.ParseFloat(m[3], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if m[2] == "A" {
			amps[m[1]] = v
		} else {
			volts[m[1]] = v
		}
	}

	d := &powerPMIC{Rails: []powerRail{}}
	for name, a := range amps {
		v, ok := volts[name]
		if !ok {
			continue
		}
		w := a * v
		d.Rails = append(d.Rails, powerRail{Name: name, Volts: v, Amps: a, Watts: powerRound(w)})
		d.TotalW += w
	}
	if len(d.Rails) == 0 {
		return nil
	}
	sort.Slice(d.Rails, func(i, j int) bool { return d.Rails[i].Name < d.Rails[j].Name })
	d.TotalW = powerRound(d.TotalW)
	if v, ok := volts[powerExt5V]; ok {
		d.Ext5VV = &v
	}
	return d
}

// powerRound rounds watts to 0.1 mW, well below what the PMIC ADC resolves.
func powerRound(w float64) float64 { return math.Round(w*1e4) / 1e4 }
