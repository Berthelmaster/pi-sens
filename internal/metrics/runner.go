package metrics

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CommandRunner runs an external command and returns its standard output.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands with os/exec. The command is killed when ctx is
// done.
type ExecRunner struct{}

// Run implements CommandRunner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Do not hang on children that keep the output pipes open after a kill.
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// FakeResponse is the canned result of one command in a FakeRunner.
type FakeResponse struct {
	Output string
	Err    error
}

// FakeRunner is a CommandRunner for tests. Responses is keyed by the command
// line joined with single spaces, e.g. "vcgencmd measure_temp". Commands
// without a response fail.
type FakeRunner struct {
	Responses map[string]FakeResponse

	mu    sync.Mutex
	calls []string
}

// Run implements CommandRunner.
func (f *FakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, ok := f.Responses[line]
	if !ok {
		return nil, fmt.Errorf("fake runner: no response for %q", line)
	}
	return []byte(resp.Output), resp.Err
}

// Calls returns the command lines run so far, in order.
func (f *FakeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
