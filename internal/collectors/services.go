package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// servicesCollector is the registered instance, kept so SetWatchServices can
// configure it.
var servicesCollector = &services{}

func init() { metrics.Register(servicesCollector) }

// SetWatchServices sets the units whose state the services collector reports
// on every sample, e.g. []string{"ssh", "pisens.service"}. Names not ending
// in ".service" get it appended. Call it before the sampler starts.
func SetWatchServices(names []string) {
	servicesCollector.setWatch(names)
}

// services reports the state of the systemd services from
// "systemctl list-units --type=service --all --output=json": how many are
// active and failed, which ones failed, and the state of a watch list.
//
// systemctl is run at most every servicesMinInterval; samples in between
// repeat the last result. Older systemd releases without JSON output for
// list-units are read from the plain table instead.
//
// Units that are only referenced but have no unit file (load state
// "not-found") are left out of the counts and the failed list.
//
// Without systemctl, or without a running systemd (a container, a chroot), the
// section is {"available": false} with no series. That is logged once and
// systemctl is not run again.
type services struct {
	mu    sync.Mutex
	watch []string

	// Only touched by Collect, which the sampler never runs concurrently.
	unavailable     bool
	jsonUnsupported bool
	lastRun         time.Time
	last            metrics.Result
	lastErr         error
}

// servicesMinInterval is the shortest time between two systemctl runs.
const servicesMinInterval = 30 * time.Second

var (
	servicesJSONArgs  = []string{"list-units", "--type=service", "--all", "--output=json"}
	servicesPlainArgs = []string{"list-units", "--type=service", "--all", "--plain", "--no-legend", "--no-pager"}
)

type servicesData struct {
	Available bool `json:"available"`
	Active    int  `json:"active"`
	Failed    int  `json:"failed"`
	Total     int  `json:"total"`
	// FailedUnits is sorted by unit name.
	FailedUnits []servicesUnit `json:"failed_units"`
	// Watched follows the order of the watch list. A unit systemd does not
	// know is reported as not-found, inactive and dead, like systemctl show
	// does.
	Watched []servicesUnit `json:"watched"`
}

type servicesUnavailable struct {
	Available bool `json:"available"`
}

type servicesUnit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description,omitempty"`
}

func (*services) Name() string { return "services" }

func (s *services) setWatch(names []string) {
	var watch []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !strings.HasSuffix(n, ".service") {
			n += ".service"
		}
		if !seen[n] {
			seen[n] = true
			watch = append(watch, n)
		}
	}
	s.mu.Lock()
	s.watch = watch
	// The next sample must show the new list.
	s.lastRun = time.Time{}
	s.mu.Unlock()
}

func (s *services) Collect(ctx context.Context, env *metrics.Env) (metrics.Result, error) {
	if s.unavailable {
		return metrics.Result{Data: servicesUnavailable{}}, nil
	}

	s.mu.Lock()
	watch := s.watch
	fresh := !s.lastRun.IsZero() && env.Now.Sub(s.lastRun) >= 0 && env.Now.Sub(s.lastRun) < servicesMinInterval
	s.mu.Unlock()
	if fresh {
		return s.last, s.lastErr
	}

	units, err := s.list(ctx, env)
	if errors.Is(err, errServicesUnavailable) {
		s.unavailable = true
		return metrics.Result{Data: servicesUnavailable{}}, nil
	}
	var res metrics.Result
	if err == nil {
		res = servicesResult(units, watch)
	}
	// A cancelled or timed out run is not remembered, so the next sample
	// tries again.
	if ctx.Err() == nil {
		s.mu.Lock()
		s.lastRun = env.Now
		s.mu.Unlock()
		s.last, s.lastErr = res, err
	}
	return res, err
}

// errServicesUnavailable means systemctl cannot be used on this machine.
var errServicesUnavailable = errors.New("systemctl is not available")

// list runs systemctl and parses its output, preferring JSON.
func (s *services) list(ctx context.Context, env *metrics.Env) ([]servicesUnit, error) {
	if env.Runner == nil {
		return nil, errServicesUnavailable
	}
	if !s.jsonUnsupported {
		out, err := s.run(ctx, env, servicesJSONArgs)
		if err == nil {
			if trimmed := strings.TrimSpace(string(out)); trimmed == "" || trimmed[0] == '[' {
				return servicesParseJSON(out)
			}
			// systemd before 246 accepts --output=json (it is a journal
			// format) but ignores it for list-units and prints the table.
			units, perr := servicesParsePlain(out)
			if perr == nil {
				s.jsonUnsupported = true
			}
			return units, perr
		}
		if errors.Is(err, errServicesUnavailable) || ctx.Err() != nil {
			return nil, err
		}
		// Some other failure, likely an unknown option. Try the plain
		// table; if that fails too, the JSON error is the one to report.
		plain, perr := s.run(ctx, env, servicesPlainArgs)
		if perr != nil {
			if errors.Is(perr, errServicesUnavailable) {
				return nil, perr
			}
			return nil, err
		}
		units, perr := servicesParsePlain(plain)
		if perr != nil {
			return nil, err
		}
		s.jsonUnsupported = true
		return units, nil
	}
	out, err := s.run(ctx, env, servicesPlainArgs)
	if err != nil {
		return nil, err
	}
	return servicesParsePlain(out)
}

// run runs systemctl and turns the failures that mean it cannot be used at
// all into errServicesUnavailable, logging that once.
func (s *services) run(ctx context.Context, env *metrics.Env, args []string) ([]byte, error) {
	out, err := env.Runner.Run(ctx, "systemctl", args...)
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	if servicesMissing(err, out) {
		slog.Default().Warn("systemctl is not usable, service status is unavailable", "err", err.Error())
		return nil, errServicesUnavailable
	}
	if msg := strings.Join(strings.Fields(string(out)), " "); msg != "" {
		return nil, fmt.Errorf("%w: %s", err, msg)
	}
	return nil, err
}

// servicesMissing reports whether a failed systemctl run means it cannot be
// used at all: the binary is missing, or there is no systemd to talk to.
func servicesMissing(err error, out []byte) bool {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return true
	}
	msg := strings.ToLower(err.Error() + " " + string(out))
	for _, s := range []string{
		"not been booted with systemd", // PID 1 is not systemd
		"failed to connect to bus",     // no D-Bus / systemd socket
		"failed to connect to system scope bus",
		"running in chroot",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func servicesParseJSON(out []byte) ([]servicesUnit, error) {
	if strings.TrimSpace(string(out)) == "" {
		return nil, nil
	}
	var units []servicesUnit
	if err := json.Unmarshal(out, &units); err != nil {
		return nil, fmt.Errorf("systemctl list-units: %w", err)
	}
	for _, u := range units {
		if u.Unit == "" {
			return nil, errors.New("systemctl list-units: unit without a name")
		}
	}
	return units, nil
}

// servicesParsePlain reads the systemctl list-units table:
//
//	UNIT                   LOAD      ACTIVE SUB     DESCRIPTION
//	● dphys-swapfile.service loaded  failed failed  dphys-swapfile - set up...
//
// with or without --plain and --no-legend. Lines that are not a .service unit
// (the header, the legend, the summary) are skipped.
func servicesParsePlain(out []byte) ([]servicesUnit, error) {
	var units []servicesUnit
	header := false
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && (f[0] == "●" || f[0] == "*") {
			f = f[1:]
		}
		if len(f) > 0 && f[0] == "UNIT" {
			header = true
			continue
		}
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		units = append(units, servicesUnit{
			Unit:        f[0],
			Load:        f[1],
			Active:      f[2],
			Sub:         f[3],
			Description: strings.Join(f[4:], " "),
		})
	}
	if units == nil && !header && strings.TrimSpace(string(out)) != "" {
		return nil, fmt.Errorf("systemctl list-units: unexpected output %q", servicesTruncate(string(out)))
	}
	return units, nil
}

func servicesTruncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

func servicesResult(units []servicesUnit, watch []string) metrics.Result {
	d := servicesData{Available: true, FailedUnits: []servicesUnit{}, Watched: []servicesUnit{}}
	byName := make(map[string]servicesUnit, len(units))
	for _, u := range units {
		byName[u.Unit] = u
		if u.Load == "not-found" {
			continue
		}
		d.Total++
		switch u.Active {
		case "active":
			d.Active++
		case "failed":
			d.Failed++
			d.FailedUnits = append(d.FailedUnits, u)
		}
	}
	sort.Slice(d.FailedUnits, func(i, j int) bool { return d.FailedUnits[i].Unit < d.FailedUnits[j].Unit })
	for _, name := range watch {
		u, ok := byName[name]
		if !ok {
			u = servicesUnit{Unit: name, Load: "not-found", Active: "inactive", Sub: "dead"}
		}
		d.Watched = append(d.Watched, u)
	}
	return metrics.Result{
		Data:   d,
		Series: map[string]float64{"services.failed": float64(d.Failed)},
	}
}
