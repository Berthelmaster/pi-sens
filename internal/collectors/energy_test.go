package collectors

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/energy"
	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

var energyT0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

// setEnergy installs a meter in a temporary data directory for one test.
func setEnergy(t *testing.T, price float64, currency string) *energy.Meter {
	t.Helper()
	m, err := energy.Open(t.TempDir(), energy.Options{Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	SetEnergy(m, price, currency)
	t.Cleanup(func() { SetEnergy(nil, 0, "") })
	return m
}

// energyFeed runs the power collector against a Pi 5 that draws 2.5 W every
// 2 seconds for an hour, which comes to 0.0025 kWh.
func energyFeed(t *testing.T) time.Time {
	t.Helper()
	pmic := &metrics.FakeResponse{Output: "" +
		"  VDD_CORE_A current(7)=2.00000000A\n" +
		"   1V8_SYS_A current(2)=0.50000000A\n" +
		"  VDD_CORE_V volt(15)=0.80000000V\n" +
		"   1V8_SYS_V volt(10)=1.80000000V\n"}
	runner := &metrics.FakeRunner{Responses: powerResponses(t, pmic)}
	p := &power{}
	now := energyT0
	for ; !now.After(energyT0.Add(time.Hour)); now = now.Add(2 * time.Second) {
		if _, err := p.Collect(context.Background(), &metrics.Env{Runner: runner, Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	return now.Add(-2 * time.Second)
}

func energyCollect(t *testing.T, now time.Time) (metrics.Result, string) {
	t.Helper()
	res, err := (&energyCollector{}).Collect(context.Background(), &metrics.Env{Now: now})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(b)
}

func TestEnergyFromPowerReadings(t *testing.T) {
	setEnergy(t, 0, "")
	end := energyFeed(t)

	res, got := energyCollect(t, end)
	want := `{"available":true,"power_w":2.5,"today_kwh":0.0025,"yesterday_kwh":0,` +
		`"month_kwh":0.0025,"total_kwh":0.0025,"since":"2026-11-07T12:00:00Z"}`
	if got != want {
		t.Errorf("JSON = %s\nwant %s", got, want)
	}
	if strings.Contains(got, "cost") || strings.Contains(got, "currency") {
		t.Errorf("JSON = %s, want no cost without a price", got)
	}
	wantSeries := map[string]float64{"energy.today_kwh": 0.0025, "energy.month_kwh": 0.0025}
	if !reflect.DeepEqual(res.Series, wantSeries) {
		t.Errorf("Series = %v, want %v", res.Series, wantSeries)
	}
}

func TestEnergyCost(t *testing.T) {
	setEnergy(t, 2.4, "DKK")
	end := energyFeed(t)

	_, got := energyCollect(t, end)
	want := `{"available":true,"power_w":2.5,"today_kwh":0.0025,"yesterday_kwh":0,` +
		`"month_kwh":0.0025,"total_kwh":0.0025,"since":"2026-11-07T12:00:00Z",` +
		`"today_cost":0.006,"month_cost":0.006,"currency":"DKK"}`
	if got != want {
		t.Errorf("JSON = %s\nwant %s", got, want)
	}
}

func TestEnergyNoPowerReadingForAWhile(t *testing.T) {
	setEnergy(t, 0, "")
	end := energyFeed(t)

	// The PMIC stopped answering: the totals stay, the power draw goes.
	_, got := energyCollect(t, end.Add(10*time.Minute))
	if !strings.Contains(got, `"available":true`) || strings.Contains(got, "power_w") {
		t.Errorf("JSON = %s, want available totals without power_w", got)
	}
}

func TestEnergyUnavailable(t *testing.T) {
	t.Run("no meter", func(t *testing.T) {
		SetEnergy(nil, 0, "")
		res, got := energyCollect(t, energyT0)
		if got != `{"available":false}` || len(res.Series) != 0 {
			t.Errorf("JSON = %s, series = %v; want unavailable and no series", got, res.Series)
		}
	})

	t.Run("no PMIC", func(t *testing.T) {
		// A Pi 4: the power collector works but never has a PMIC total.
		setEnergy(t, 2.4, "DKK")
		resp := metrics.FakeResponse{Output: "error=2 error_msg=\"Command not registered\"\n"}
		runner := &metrics.FakeRunner{Responses: powerResponses(t, &resp)}
		p := &power{}
		for i := 0; i < 3; i++ {
			now := energyT0.Add(time.Duration(i) * 2 * time.Second)
			if _, err := p.Collect(context.Background(), &metrics.Env{Runner: runner, Now: now}); err != nil {
				t.Fatal(err)
			}
			res, got := energyCollect(t, now)
			if got != `{"available":false}` || len(res.Series) != 0 {
				t.Errorf("JSON = %s, series = %v; want unavailable and no series", got, res.Series)
			}
		}
	})
}
