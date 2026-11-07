// Package history keeps metric series in a SQLite database so the dashboard
// can show more than the live window.
//
// The database lives at <data-dir>/pisens.db and uses modernc.org/sqlite, a
// pure Go driver, so pisens still builds with CGO_ENABLED=0. It runs in WAL
// mode with synchronous=NORMAL, and the Writer batches samples into one
// transaction per history interval, which keeps SD card writes low.
//
// Every series is stored by key in the series table, so a collector that
// adds a new key needs no code here: the key gets a row the first time it is
// written.
package history

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// FileName is the name of the database file inside the data directory.
const FileName = "pisens.db"

// pragmas are applied to every connection. busy_timeout covers the rare case
// of a reader holding a lock while we write.
var pragmas = []string{
	"busy_timeout(5000)",
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
}

// migrations are applied in order; migration i brings the schema to version
// i+1. Never edit one that has shipped, append a new one instead.
var migrations = []string{
	// 1: series keys and raw samples.
	`CREATE TABLE series (
		id  INTEGER PRIMARY KEY,
		key TEXT NOT NULL UNIQUE
	);
	CREATE TABLE samples_raw (
		series_id INTEGER NOT NULL,
		ts        INTEGER NOT NULL, -- unix seconds
		value     REAL NOT NULL,
		PRIMARY KEY (series_id, ts)
	) WITHOUT ROWID;`,
}

// Sample is the value of every series at one point in time.
type Sample struct {
	Time   time.Time
	Values map[string]float64
}

// Store is the history database.
type Store struct {
	db   *sql.DB
	path string

	mu  sync.Mutex       // serialises Insert
	ids map[string]int64 // series key to id, guarded by mu
}

// Open opens the database in dir, creating the directory, the file and the
// schema as needed.
func Open(ctx context.Context, dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("history: create data directory: %w", err)
	}
	path, err := filepath.Abs(filepath.Join(dir, FileName))
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("history: open %s: %w", path, err)
	}
	s := &Store{db: db, path: path, ids: map[string]int64{}}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("history: open %s: %w", path, err)
	}
	return s, nil
}

// dsn builds a SQLite URI for path. Escaping the path keeps characters such
// as '?' and '#' in directory names from being read as URI syntax.
func dsn(path string) string {
	q := url.Values{"_pragma": pragmas}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	return u.String()
}

func (s *Store) init(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return fmt.Errorf("journal mode is %q, want wal", mode)
	}
	if err := migrate(ctx, s.db); err != nil {
		return err
	}
	return s.loadIDs(ctx)
}

// Path returns the absolute path of the database file.
func (s *Store) Path() string { return s.path }

// Close closes the database. SQLite checkpoints the WAL into the main file
// when the last connection closes.
func (s *Store) Close() error { return s.db.Close() }

// migrate brings the schema up to date, one transaction per migration.
func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL -- unix seconds
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var version int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this pisens supports (%d)", version, len(migrations))
	}

	for i := version; i < len(migrations); i++ {
		if err := applyMigration(ctx, db, i+1, migrations[i]); err != nil {
			return fmt.Errorf("migrate to version %d: %w", i+1, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, version int, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
		version, time.Now().Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) loadIDs(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT id, key FROM series")
	if err != nil {
		return fmt.Errorf("load series: %w", err)
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return fmt.Errorf("load series: %w", err)
		}
		s.ids[key] = id
	}
	return rows.Err()
}

// Insert writes samples in a single transaction. Keys that are not in the
// database yet are added. Timestamps are stored in whole seconds, so a later
// sample in the same second replaces an earlier one. NaN and infinite values
// are skipped.
func (s *Store) Insert(ctx context.Context, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history: begin: %w", err)
	}
	defer tx.Rollback()

	addSeries, err := tx.PrepareContext(ctx, `INSERT INTO series (key) VALUES (?)
		ON CONFLICT (key) DO UPDATE SET key = excluded.key RETURNING id`)
	if err != nil {
		return fmt.Errorf("history: prepare: %w", err)
	}
	defer addSeries.Close()
	addSample, err := tx.PrepareContext(ctx, `INSERT INTO samples_raw (series_id, ts, value) VALUES (?, ?, ?)
		ON CONFLICT (series_id, ts) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return fmt.Errorf("history: prepare: %w", err)
	}
	defer addSample.Close()

	// IDs of keys added in this transaction only become valid once it commits.
	added := map[string]int64{}
	for _, smp := range samples {
		ts := smp.Time.Unix()
		for key, v := range smp.Values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			id, ok := s.ids[key]
			if !ok {
				id, ok = added[key]
			}
			if !ok {
				if err := addSeries.QueryRowContext(ctx, key).Scan(&id); err != nil {
					return fmt.Errorf("history: add series %q: %w", key, err)
				}
				added[key] = id
			}
			if _, err := addSample.ExecContext(ctx, id, ts, v); err != nil {
				return fmt.Errorf("history: insert %q: %w", key, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history: commit: %w", err)
	}
	for k, id := range added {
		s.ids[k] = id
	}
	return nil
}
