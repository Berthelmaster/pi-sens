package config

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These tests keep deploy/pisens.env.example and the configuration table in
// the README in step with the settings in this package.

const (
	envExamplePath = "../../deploy/pisens.env.example"
	readmePath     = "../../README.md"
)

// settingsWithDefault are the settings that have a built-in default. For
// these the example file and the README must show that default. The other
// settings are off unless set, and the example file shows a sample value.
var settingsWithDefault = []string{
	EnvListen,
	EnvInterval,
	EnvHistoryInterval,
	EnvHistoryRawRetention,
	EnvHistory1mRetention,
	EnvHistory15mRetention,
	EnvNotifyCooldown,
	EnvPrometheus,
}

// envNames returns the values of every Env* constant in config.go, so a new
// setting is covered without touching this test.
func envNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "config.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, id := range vs.Names {
				if !strings.HasPrefix(id.Name, "Env") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				names = append(names, v)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("found no Env constants in config.go")
	}
	return names
}

// exampleValues returns the commented-out KEY=value lines of the example file.
func exampleValues(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(envExamplePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	values := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line, ok := strings.CutPrefix(s.Text(), "#PISENS_")
		if !ok {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Errorf("example line %q has no =", s.Text())
			continue
		}
		values["PISENS_"+key] = value
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

var readmeRow = regexp.MustCompile("^\\|\\s*`--[^`]+`\\s*\\|\\s*`(PISENS_[A-Z0-9_]+)`\\s*\\|\\s*([^|]*?)\\s*\\|")

// readmeDefaults returns the default column of the README configuration
// table by environment variable, with the backticks and any note such as
// "(7 days)" removed.
func readmeDefaults(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		m := readmeRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		def := m[2]
		if strings.HasPrefix(def, "`") {
			if end := strings.Index(def[1:], "`"); end >= 0 {
				def = def[1 : end+1]
			}
		}
		defaults[m[1]] = def
	}
	return defaults
}

func TestEnvExampleListsEverySetting(t *testing.T) {
	example := exampleValues(t)
	readme := readmeDefaults(t)
	for _, name := range envNames(t) {
		if _, ok := example[name]; !ok {
			t.Errorf("%s is missing from %s; add a commented-out #%s= line", name, envExamplePath, name)
		}
		if _, ok := readme[name]; !ok {
			t.Errorf("%s is missing from the configuration table in %s", name, readmePath)
		}
	}
}

func TestEnvExampleShowsDefaults(t *testing.T) {
	example := exampleValues(t)
	readme := readmeDefaults(t)

	env := map[string]string{}
	for _, name := range settingsWithDefault {
		v, ok := example[name]
		if !ok {
			t.Errorf("%s is missing from %s", name, envExamplePath)
			continue
		}
		if readme[name] != v {
			t.Errorf("%s: %s shows %q, the README default is %q", name, envExamplePath, v, readme[name])
		}
		env[name] = v
	}

	// Loading the example values must give the same configuration as loading
	// nothing, otherwise the example shows something other than the default.
	want, err := Load(nil, envFrom(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(nil, envFrom(env), io.Discard)
	if err != nil {
		t.Fatalf("loading the example values: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the example values do not match the built-in defaults:\n got %+v\nwant %+v", got, want)
	}
}
