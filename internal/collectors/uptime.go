package collectors

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

func init() { metrics.Register(uptime{}) }

// uptime reports how long the system has been running, from /proc/uptime.
type uptime struct{}

type uptimeData struct {
	Seconds  float64   `json:"seconds"`
	BootTime time.Time `json:"boot_time"`
}

func (uptime) Name() string { return "uptime" }

func (uptime) Collect(_ context.Context, env *metrics.Env) (metrics.Result, error) {
	b, err := fs.ReadFile(env.FS, "proc/uptime")
	if err != nil {
		return metrics.Result{}, err
	}
	// Format: "<uptime seconds> <idle seconds>\n"
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return metrics.Result{}, fmt.Errorf("proc/uptime: empty")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return metrics.Result{}, fmt.Errorf("proc/uptime: %w", err)
	}

	boot := env.Now.Add(-time.Duration(secs * float64(time.Second))).Truncate(time.Second)
	return metrics.Result{
		Data:   uptimeData{Seconds: secs, BootTime: boot},
		Series: map[string]float64{"system.uptime_s": secs},
	}, nil
}
