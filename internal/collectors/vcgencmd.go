package collectors

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"reflect"
	"strings"
	"sync"

	"github.com/Berthelmaster/pi-sens/internal/metrics"
)

// errVcgencmdUnavailable is returned by vcgencmd when the tool cannot be used
// on this machine. Collectors report their section as unavailable instead of
// failing.
var errVcgencmdUnavailable = errors.New("vcgencmd is not available")

// vcgencmdState remembers, per command runner, that vcgencmd turned out to be
// unusable, so it is not run (and logged) again on every sample. Keying by
// runner keeps tests that each use their own metrics.FakeRunner independent;
// in production there is a single runner.
var vcgencmdState = struct {
	sync.Mutex
	unavailable map[metrics.CommandRunner]bool
}{unavailable: map[metrics.CommandRunner]bool{}}

// vcgencmd runs "vcgencmd args..." through env.Runner and returns its trimmed
// output.
//
// If vcgencmd is not installed, or cannot talk to the firmware, it returns
// errVcgencmdUnavailable. That is detected on the first failing call, logged
// once, and remembered: later calls return errVcgencmdUnavailable without
// running anything. Other failures (a timeout, an unknown command) are
// returned with vcgencmd's output appended, since that is where it prints
// the firmware's error ("error=2 error_msg=\"Command not registered\"").
//
// vcgencmd talks to the firmware through /dev/vcio, which is owned by the
// video group. The service user must be a member of that group
// (usermod -aG video <user>), otherwise every call fails with a permission
// error and the Pi hardware sections show as unavailable.
func vcgencmd(ctx context.Context, env *metrics.Env, args ...string) (string, error) {
	r := env.Runner
	if r == nil {
		return "", errVcgencmdUnavailable
	}
	// Non-comparable runners cannot be map keys; they just are not remembered.
	key := reflect.TypeOf(r).Comparable()

	if key {
		vcgencmdState.Lock()
		gone := vcgencmdState.unavailable[r]
		vcgencmdState.Unlock()
		if gone {
			return "", errVcgencmdUnavailable
		}
	}

	out, err := r.Run(ctx, "vcgencmd", args...)
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	if ctx.Err() != nil {
		return "", err
	}
	if !vcgencmdMissing(err, out) {
		if msg := strings.Join(strings.Fields(string(out)), " "); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}

	first := true
	if key {
		vcgencmdState.Lock()
		first = !vcgencmdState.unavailable[r]
		vcgencmdState.unavailable[r] = true
		vcgencmdState.Unlock()
	}
	if first {
		slog.Default().Warn("vcgencmd is not usable, Raspberry Pi hardware metrics are unavailable; "+
			"on a Pi, install it and add the service user to the video group",
			"err", err.Error())
	}
	return "", errVcgencmdUnavailable
}

// vcgencmdMissing reports whether a failed vcgencmd run means the tool cannot
// be used at all: the binary is missing or not executable, or it cannot open
// the firmware interface (/dev/vcio, or /dev/vchiq on older releases), which
// is what happens without permission or when not running on a Pi.
func vcgencmdMissing(err error, out []byte) bool {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return true
	}
	msg := strings.ToLower(err.Error() + " " + string(out))
	for _, s := range []string{"vcio", "vchi", "permission denied", "operation not permitted"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
