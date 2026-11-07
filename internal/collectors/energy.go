package collectors

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/energy"
	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(&energyCollector{}) }

// energyConfig is what SetEnergy configures. It is swapped as a whole so the
// power and energy collectors, which run concurrently, always see a
// consistent one.
type energyConfig struct {
	meter    *energy.Meter
	price    float64
	currency string
}

var energyCfg atomic.Pointer[energyConfig]

// SetEnergy hands the collectors the meter that adds up the Pi's energy use.
// The power collector feeds it every PMIC total power reading and the energy
// collector reports its totals. A price above zero, per kWh, adds the cost of
// today and this month in currency (free text such as "DKK", may be empty).
// Without a meter the energy section is unavailable. Call it before the
// sampler starts.
func SetEnergy(m *energy.Meter, price float64, currency string) {
	if m == nil {
		energyCfg.Store(nil)
		return
	}
	energyCfg.Store(&energyConfig{meter: m, price: price, currency: currency})
}

// energyAdd is called by the power collector with each PMIC total power
// reading.
func energyAdd(t time.Time, w float64) {
	if c := energyCfg.Load(); c != nil {
		c.meter.Add(t, w)
	}
}

// energyCollector reports the totals of the energy meter that the power
// collector feeds (see SetEnergy). The integration happens in the power
// collector, at the time of each reading, rather than here: collectors run
// concurrently, so this one cannot wait for the power reading of the same
// round. Its section can therefore be one sample behind, which for kWh
// totals is a few milliwatt-hours at most.
//
// Without PMIC readings (a Pi before the Pi 5, or not a Pi at all) the
// section is {"available": false} with no series.
type energyCollector struct{}

type energyData struct {
	Available    bool       `json:"available"`
	PowerW       *float64   `json:"power_w,omitempty"`
	TodayKWh     *float64   `json:"today_kwh,omitempty"`
	YesterdayKWh *float64   `json:"yesterday_kwh,omitempty"`
	MonthKWh     *float64   `json:"month_kwh,omitempty"`
	TotalKWh     *float64   `json:"total_kwh,omitempty"`
	Since        *time.Time `json:"since,omitempty"`
	TodayCost    *float64   `json:"today_cost,omitempty"`
	MonthCost    *float64   `json:"month_cost,omitempty"`
	Currency     *string    `json:"currency,omitempty"`
}

func (*energyCollector) Name() string { return "energy" }

func (*energyCollector) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	c := energyCfg.Load()
	if c == nil {
		return metrics.Result{Data: energyData{Available: false}}, nil
	}
	t := c.meter.Totals(env.Now)
	if !t.Live {
		return metrics.Result{Data: energyData{Available: false}}, nil
	}

	today, month := energyRound(t.TodayKWh), energyRound(t.MonthKWh)
	yesterday, total := energyRound(t.YesterdayKWh), energyRound(t.TotalKWh)
	since := t.Since.UTC()
	data := energyData{
		Available:    true,
		PowerW:       t.PowerW,
		TodayKWh:     &today,
		YesterdayKWh: &yesterday,
		MonthKWh:     &month,
		TotalKWh:     &total,
		Since:        &since,
	}
	if c.price > 0 {
		todayCost, monthCost := costRound(t.TodayKWh*c.price), costRound(t.MonthKWh*c.price)
		currency := c.currency
		data.TodayCost, data.MonthCost, data.Currency = &todayCost, &monthCost, &currency
	}
	series := map[string]float64{
		"energy.today_kwh": today,
		"energy.month_kwh": month,
	}
	return metrics.Result{Data: data, Series: series}, nil
}

// energyRound rounds kWh to 1 mWh, below what a few seconds at the Pi's
// power draw add up to.
func energyRound(kwh float64) float64 { return math.Round(kwh*1e6) / 1e6 }

// costRound rounds a cost to four decimals; the dashboard rounds further.
func costRound(v float64) float64 { return math.Round(v*1e4) / 1e4 }
