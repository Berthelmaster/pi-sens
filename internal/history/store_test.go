package history

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func openTest(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", dir, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// point is one stored value.
type point struct {
	TS    int64
	Value float64
}

// readAll returns every stored sample grouped by series key.
func readAll(t *testing.T, s *Store) map[string][]point {
	t.Helper()
	rows, err := s.db.Query(`SELECT series.key, samples_raw.ts, samples_raw.value
		FROM samples_raw JOIN series ON series.id = samples_raw.series_id
		ORDER BY series.key, samples_raw.ts`)
	if err != nil {
		t.Fatalf("query samples: %v", err)
	}
	defer rows.Close()
	got := map[string][]point{}
	for rows.Next() {
		var key string
		var p point
		if err := rows.Scan(&key, &p.TS, &p.Value); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[key] = append(got[key], p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return got
}

func seriesIDs(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	rows, err := s.db.Query("SELECT key, id FROM series")
	if err != nil {
		t.Fatalf("query series: %v", err)
	}
	defer rows.Close()
	ids := map[string]int64{}
	for rows.Next() {
		var key string
		var id int64
		if err := rows.Scan(&key, &id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids[key] = id
	}
	return ids
}

func TestOpenCreatesDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	s := openTest(t, dir)

	want := filepath.Join(dir, FileName)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("database file: %v", err)
	}
	if !filepath.IsAbs(s.Path()) || filepath.Base(s.Path()) != FileName {
		t.Errorf("Path() = %q, want an absolute path to %s", s.Path(), FileName)
	}

	var mode string
	var sync int
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	if sync != 1 { // NORMAL
		t.Errorf("synchronous = %d, want 1 (NORMAL)", sync)
	}

	var version int
	if err := s.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Errorf("schema version = %d, want %d", version, len(migrations))
	}
}

func TestOpenPathNeedsEscaping(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "odd ?name#%")
	s := openTest(t, dir)
	if err := s.Insert(context.Background(), []Sample{{Time: t0, Values: map[string]float64{"a": 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("database file: %v", err)
	}
}

func TestReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, []Sample{{Time: t0, Values: map[string]float64{"cpu.usage_pct": 12.5}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openTest(t, dir)
	before := seriesIDs(t, s)
	if err := s.Insert(ctx, []Sample{{Time: t0.Add(10 * time.Second), Values: map[string]float64{"cpu.usage_pct": 20}}}); err != nil {
		t.Fatal(err)
	}
	if after := seriesIDs(t, s); !reflect.DeepEqual(after, before) {
		t.Errorf("series after reopen = %v, want unchanged %v", after, before)
	}
	want := map[string][]point{"cpu.usage_pct": {{t0.Unix(), 12.5}, {t0.Unix() + 10, 20}}}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}

	var migrated int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	if migrated != len(migrations) {
		t.Errorf("schema_migrations has %d rows after reopen, want %d", migrated, len(migrations))
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)", len(migrations)+1); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(context.Background(), dir)
	if err == nil {
		s.Close()
		t.Fatal("Open() error = nil, want an error for a newer schema")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("Open() error = %v, want it to mention the newer schema", err)
	}
}

func TestInsert(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()

	err := s.Insert(ctx, []Sample{
		{Time: t0, Values: map[string]float64{"cpu.usage_pct": 10, "memory.used_bytes": 1e9}},
		{Time: t0.Add(2 * time.Second), Values: map[string]float64{"cpu.usage_pct": 11, "memory.used_bytes": 1.1e9}},
	})
	if err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	want := map[string][]point{
		"cpu.usage_pct":     {{t0.Unix(), 10}, {t0.Unix() + 2, 11}},
		"memory.used_bytes": {{t0.Unix(), 1e9}, {t0.Unix() + 2, 1.1e9}},
	}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

func TestInsertNewKeys(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()

	if err := s.Insert(ctx, []Sample{{Time: t0, Values: map[string]float64{"a": 1}}}); err != nil {
		t.Fatal(err)
	}
	first := seriesIDs(t, s)

	// A key that shows up later, e.g. a newly attached disk, gets its own
	// series without touching the existing ones.
	err := s.Insert(ctx, []Sample{
		{Time: t0.Add(10 * time.Second), Values: map[string]float64{"a": 2, "b": 20}},
		{Time: t0.Add(20 * time.Second), Values: map[string]float64{"b": 30, "c": 300}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := seriesIDs(t, s)
	if len(ids) != 3 || ids["a"] != first["a"] {
		t.Errorf("series = %v, want a, b and c with a still %d", ids, first["a"])
	}
	want := map[string][]point{
		"a": {{t0.Unix(), 1}, {t0.Unix() + 10, 2}},
		"b": {{t0.Unix() + 10, 20}, {t0.Unix() + 20, 30}},
		"c": {{t0.Unix() + 20, 300}},
	}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

func TestInsertSameSecondReplaces(t *testing.T) {
	s := openTest(t, t.TempDir())
	err := s.Insert(context.Background(), []Sample{
		{Time: t0, Values: map[string]float64{"a": 1}},
		{Time: t0.Add(500 * time.Millisecond), Values: map[string]float64{"a": 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]point{"a": {{t0.Unix(), 2}}}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

func TestInsertSkipsNonFinite(t *testing.T) {
	s := openTest(t, t.TempDir())
	err := s.Insert(context.Background(), []Sample{{Time: t0, Values: map[string]float64{
		"ok": 1, "nan": math.NaN(), "inf": math.Inf(1),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]point{"ok": {{t0.Unix(), 1}}}
	if got := readAll(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("samples = %v, want %v", got, want)
	}
}

func TestInsertFailureKeepsNoNewKeys(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	// Make the second sample fail after the first one has added its key.
	_, err := s.db.Exec(`CREATE TRIGGER fail BEFORE INSERT ON samples_raw WHEN NEW.value < 0
		BEGIN SELECT RAISE(ABORT, 'negative'); END`)
	if err != nil {
		t.Fatal(err)
	}
	batch := []Sample{
		{Time: t0, Values: map[string]float64{"a": 1}},
		{Time: t0.Add(time.Second), Values: map[string]float64{"b": -1}},
	}
	if err := s.Insert(ctx, batch); err == nil {
		t.Fatal("Insert() error = nil, want the trigger's error")
	}
	if len(s.ids) != 0 {
		t.Errorf("cached ids = %v after a failed insert, want none", s.ids)
	}
	if got := seriesIDs(t, s); len(got) != 0 {
		t.Errorf("series = %v after a failed insert, want none", got)
	}

	// The keys are added for real on the next successful write.
	if _, err := s.db.Exec("DROP TRIGGER fail"); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if got := seriesIDs(t, s); !reflect.DeepEqual(got, s.ids) || len(got) != 2 {
		t.Errorf("series = %v, cached %v, want both a and b", got, s.ids)
	}
}
