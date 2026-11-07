//go:build !linux

package collectors

import "errors"

// syscallStatter is the real fsStatter. Only Linux is supported; elsewhere the
// package still builds so the tests can run.
type syscallStatter struct{}

func (syscallStatter) Statfs(string) (fsStats, error) {
	return fsStats{}, errors.ErrUnsupported
}
