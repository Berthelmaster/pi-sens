package collectors

import (
	"errors"
	"io/fs"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// errFS fails every open with err.
type errFS struct{ err error }

func (f errFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: f.err}
}

func TestReadValue(t *testing.T) {
	fsys := fstest.MapFS{
		"a/value": {Data: []byte("  schedutil \n")},
		"a/empty": {Data: []byte("\n")},
	}
	tests := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{"a/value", "schedutil", true},
		{"a/empty", "", true},
		{"a/missing", "", false},
		{"missing/value", "", false},
	}
	for _, tt := range tests {
		got, ok, err := readValue(fsys, tt.name)
		if err != nil {
			t.Errorf("readValue(%q) error = %v", tt.name, err)
		}
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("readValue(%q) = %q, %v, want %q, %v", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestReadValueError(t *testing.T) {
	eio := errors.New("input/output error")
	_, ok, err := readValue(errFS{eio}, "sys/class/hwmon/hwmon0/temp1_input")
	if !errors.Is(err, eio) || ok {
		t.Errorf("readValue() = ok %v, error %v, want ok false, error %v", ok, err, eio)
	}
}

func TestReadInt(t *testing.T) {
	fsys := fstest.MapFS{
		"pos":     {Data: []byte("4500\n")},
		"neg":     {Data: []byte("-12\n")},
		"garbage": {Data: []byte("<unknown>\n")},
		"empty":   {Data: []byte("")},
	}
	for name, want := range map[string]int64{"pos": 4500, "neg": -12} {
		got, ok, err := readInt(fsys, name)
		if err != nil || !ok || got != want {
			t.Errorf("readInt(%q) = %d, %v, %v, want %d, true, nil", name, got, ok, err, want)
		}
	}

	if got, ok, err := readInt(fsys, "missing"); err != nil || ok || got != 0 {
		t.Errorf("readInt(missing) = %d, %v, %v, want 0, false, nil", got, ok, err)
	}

	for _, name := range []string{"garbage", "empty"} {
		_, ok, err := readInt(fsys, name)
		var numErr *strconv.NumError
		if err == nil || ok || !errors.As(err, &numErr) {
			t.Fatalf("readInt(%q) = ok %v, error %v, want a *strconv.NumError", name, ok, err)
		}
		if !strings.HasPrefix(err.Error(), name+": ") {
			t.Errorf("readInt(%q) error = %q, want it prefixed with the path", name, err)
		}
	}

	eio := errors.New("input/output error")
	if _, ok, err := readInt(errFS{eio}, "x"); !errors.Is(err, eio) || ok {
		t.Errorf("readInt() = ok %v, error %v, want ok false, error %v", ok, err, eio)
	}
}

func TestNumberedEntries(t *testing.T) {
	fsys := fstest.MapFS{
		"cpu/cpu0/x":     {},
		"cpu/cpu10/x":    {},
		"cpu/cpu2/x":     {},
		"cpu/cpufreq/x":  {},
		"cpu/cpuidle/x":  {},
		"cpu/cpu-1/x":    {},
		"cpu/cpu/x":      {},
		"cpu/online":     {Data: []byte("0-3\n")},
		"cpu/modalias":   {},
		"empty/.keep":    {},
		"cpu/cpu3":       {Data: []byte("a file counts too\n")},
		"cpu/uevent_cpu": {},
	}
	got, err := numberedEntries(fsys, "cpu", "cpu")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 2, 3, 10}; !reflect.DeepEqual(got, want) {
		t.Errorf("numberedEntries(cpu) = %v, want %v", got, want)
	}

	for _, dir := range []string{"empty", "missing"} {
		got, err := numberedEntries(fsys, dir, "cpu")
		if err != nil || len(got) != 0 {
			t.Errorf("numberedEntries(%q) = %v, %v, want none", dir, got, err)
		}
	}

	eio := errors.New("input/output error")
	if _, err := numberedEntries(errFS{eio}, "cpu", "cpu"); !errors.Is(err, eio) {
		t.Errorf("numberedEntries() error = %v, want %v", err, eio)
	}
}
