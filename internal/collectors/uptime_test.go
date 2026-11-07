package collectors

import (
	"context"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func TestUptime(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	env := &metrics.Env{FS: os.DirFS("testdata/uptime"), Now: now}

	res, err := uptime{}.Collect(context.Background(), env)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if got := res.Series["system.uptime_s"]; got != 354127.42 {
		t.Errorf("system.uptime_s = %v, want 354127.42", got)
	}
	data, ok := res.Data.(uptimeData)
	if !ok {
		t.Fatalf("Data is %T, want uptimeData", res.Data)
	}
	wantBoot := time.Date(2026, 11, 3, 9, 37, 52, 0, time.UTC) // now - 354127.42s, truncated
	if data.Seconds != 354127.42 || !data.BootTime.Equal(wantBoot) {
		t.Errorf("Data = %+v, want seconds 354127.42 and boot time %v", data, wantBoot)
	}
}

func TestUptimeErrors(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"missing": {},
		"empty":   {"proc/uptime": {Data: []byte("\n")}},
		"garbage": {"proc/uptime": {Data: []byte("abc 12\n")}},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			env := &metrics.Env{FS: fsys, Now: time.Now()}
			if _, err := (uptime{}).Collect(context.Background(), env); err == nil {
				t.Fatal("Collect() error = nil, want an error")
			}
		})
	}
}

func TestUptimeIsRegistered(t *testing.T) {
	for _, c := range metrics.Registered() {
		if c.Name() == "uptime" {
			return
		}
	}
	t.Fatal("uptime collector is not registered")
}
