package collectors

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Helpers shared by the collectors that read single-value files from /proc
// and /sys. Each collector decides for itself what a read error means; these
// only report it.

// readValue reads a single-value file such as a sysfs attribute and trims the
// surrounding whitespace. ok is false if the file does not exist; any other
// read error is returned.
func readValue(fsys fs.FS, name string) (s string, ok bool, err error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(b)), true, nil
}

// readInt reads a file holding a base-10 integer. ok is false if the file does
// not exist. A value that is not an integer is an error prefixed with name,
// wrapping the *strconv.NumError.
func readInt(fsys fs.FS, name string) (v int64, ok bool, err error) {
	s, ok, err := readValue(fsys, name)
	if err != nil || !ok {
		return 0, false, err
	}
	v, err = strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	return v, true, nil
}

// numberedEntries returns N for every <prefix><N> entry of dir in ascending
// numeric order, skipping entries where the rest is not a non-negative number
// (cpufreq next to cpu0). A missing dir yields none.
func numberedEntries(fsys fs.FS, dir, prefix string) ([]int, error) {
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
