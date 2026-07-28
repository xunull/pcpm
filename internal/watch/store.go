package watch

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: keeps CGO_ENABLED=0 (ADR-0006, ADR-0007)
)

// schemaVersion is the shape of the database this build expects. Every release
// that changes the schema raises it and adds the corresponding migration, so an
// older database is upgraded in place rather than being silently misread.
const schemaVersion = 9

// migrations[i] takes the database from version i to version i+1. They run in
// order inside one transaction, so a failure leaves the version untouched.
var migrations = []string{
	// 0 -> 1: the Watch Targets themselves.
	`CREATE TABLE target (
		id         INTEGER PRIMARY KEY,
		pid        INTEGER NOT NULL,
		created    INTEGER NOT NULL, -- process start, unix millis
		name       TEXT    NOT NULL,
		cmdline    TEXT    NOT NULL,
		cwd        TEXT    NOT NULL,
		added_at   INTEGER NOT NULL,
		stopped_at INTEGER,          -- NULL while pcpm is still watching
		UNIQUE (pid, created)        -- (PID, Created) identifies the process
	)`,

	// 1 -> 2: the Samples. One row per process per tick, never per tree, so a
	// tree figure stays decomposable into who was responsible for it.
	//
	// WITHOUT ROWID with (target_id, at, pid) as the key: rows are then stored
	// in the order every query wants to read them — one target, a time window,
	// ascending — and the key doubles as the uniqueness that makes re-saving a
	// tick idempotent. Measured at ~38 bytes/row.
	`CREATE TABLE sample (
		target_id   INTEGER NOT NULL REFERENCES target(id),
		at          INTEGER NOT NULL, -- tick time, unix millis
		pid         INTEGER NOT NULL,
		created     INTEGER NOT NULL, -- pins the process against PID reuse
		name        TEXT    NOT NULL,
		cpu_seconds REAL    NOT NULL, -- cumulative counter, not a rate (ADR-0008)
		rss_bytes   INTEGER NOT NULL,
		PRIMARY KEY (target_id, at, pid)
	) WITHOUT ROWID`,

	// 2 -> 3: when the target's last process exited, as the collector observed
	// it. Distinct from stopped_at, which is the user's decision to stop
	// watching: a target can end without being stopped, and be stopped while
	// still running.
	`ALTER TABLE target ADD COLUMN ended_at INTEGER`,

	// 3 -> 4: downsampled history.
	//
	// A rollup row carries the CPU seconds consumed during its bucket and how
	// much time that covered — both additive, so a coarser bucket is a sum of
	// finer ones. Storing a percentage instead would not survive being
	// re-aggregated (ADR-0008).
	//
	// bucket_ms is part of the key so more than one resolution can coexist
	// later without a migration.
	`CREATE TABLE rollup (
		target_id   INTEGER NOT NULL REFERENCES target(id),
		at          INTEGER NOT NULL, -- bucket start, unix millis
		bucket_ms   INTEGER NOT NULL, -- bucket width
		pid         INTEGER NOT NULL,
		name        TEXT    NOT NULL,
		cpu_seconds REAL    NOT NULL, -- consumed during the bucket, not cumulative
		rss_bytes   INTEGER NOT NULL,
		span_ms     INTEGER NOT NULL, -- time the samples actually covered
		PRIMARY KEY (target_id, at, bucket_ms, pid)
	) WITHOUT ROWID`,

	// 4 -> 5: bookkeeping pcpm keeps about itself, such as how far the rollup
	// has got — which is what makes each rollup pass incremental.
	`CREATE TABLE meta (
		key   TEXT    PRIMARY KEY,
		value INTEGER NOT NULL
	) WITHOUT ROWID`,

	// 5 -> 6: the peak within each summarised bucket.
	//
	// Without it a burst is averaged away at the minute boundary before any
	// longer window sees it, so a three-second request inside an idle hour
	// becomes invisible — and "is anything still using this" is the question
	// the whole tool is for (ADR-0010).
	`ALTER TABLE rollup ADD COLUMN cpu_max REAL NOT NULL DEFAULT 0`,
	`ALTER TABLE rollup ADD COLUMN rss_max INTEGER NOT NULL DEFAULT 0`,

	// 7 -> 9: Traffic. Cumulative counters like cpu_seconds, not rates
	// (ADR-0008), though this counter belongs to pcpm's reading of the machine
	// rather than to the process: it starts at zero when the collector starts,
	// so a period when the collector was not running is not merely unsampled
	// but unknowable (ADR-0012). Existing rows default to zero, which is
	// indistinguishable from "no traffic" — acceptable only because it is also
	// true of them: nothing was measuring it.
	`ALTER TABLE sample ADD COLUMN net_in_bytes INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE sample ADD COLUMN net_out_bytes INTEGER NOT NULL DEFAULT 0`,
}

// Store is pcpm's local database. One file holds every tool's data, so a
// feature added later inherits somewhere to put things rather than inventing
// its own (ADR-0007).
type Store struct {
	db *sql.DB
}

// Open opens the database at path, creating it and its parent directory if
// needed, and brings its schema up to date. An existing database keeps its
// contents.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	// WAL so a reader (`watch ls`, the TUI) is never blocked by the daemon
	// writing; foreign keys on so a later table cannot reference a target that
	// is not there.
	dsn := path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate applies every migration the database has not seen yet.
func migrate(db *sql.DB) error {
	var current int
	// user_version is SQLite's own per-database integer; using it avoids a
	// bootstrap table that would itself need creating before it can be read.
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("database is at schema version %d, but this build of pcpm only knows up to %d — upgrade pcpm", current, schemaVersion)
	}
	for v := current; v < schemaVersion; v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying migration %d: %w", v+1, err)
		}
		// PRAGMA takes no bind parameters, and v+1 is a constant from our own
		// migration list, never user input.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// AddTarget starts watching a process, returning the stored target. Adding one
// that is already watched changes nothing and returns it as it stands; adding
// one that was stopped resumes it, keeping its history rather than starting a
// second record beside it.
func (s *Store) AddTarget(t Target, now time.Time) (Target, error) {
	_, err := s.db.Exec(
		`INSERT INTO target (pid, created, name, cmdline, cwd, added_at, stopped_at)
		 VALUES (?, ?, ?, ?, ?, ?, NULL)
		 ON CONFLICT (pid, created) DO UPDATE SET stopped_at = NULL, ended_at = NULL`,
		t.PID, t.Created.UnixMilli(), t.Name, t.Cmdline, t.Cwd, now.UnixMilli())
	if err != nil {
		return Target{}, err
	}
	return s.targetOf(t.PID, t.Created)
}

// StopTarget stops watching every target with this PID, returning how many were
// stopped. The rows stay: what a target was doing before it was stopped is
// still worth being able to ask about. Stopping something not being watched is
// not an error — the caller's intent is already satisfied.
func (s *Store) StopTarget(pid int32, now time.Time) (int, error) {
	res, err := s.db.Exec(
		`UPDATE target SET stopped_at = ? WHERE pid = ? AND stopped_at IS NULL`,
		now.UnixMilli(), pid)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// targetColumns is the projection every target query reads, in the order
// scanTarget expects.
const targetColumns = `id, pid, created, name, cmdline, cwd, added_at, stopped_at, ended_at`

// Targets returns every target, watched or stopped, oldest first.
func (s *Store) Targets() ([]Target, error) {
	return s.targetsWhere("")
}

// targetsWhere reads the targets matching a condition, oldest first. The
// condition is a literal from this package, never anything a user supplied.
func (s *Store) targetsWhere(condition string) ([]Target, error) {
	query := `SELECT ` + targetColumns + ` FROM target `
	if condition != "" {
		query += `WHERE ` + condition + ` `
	}
	rows, err := s.db.Query(query + `ORDER BY added_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Target
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// targetOf reads back the target for one process.
func (s *Store) targetOf(pid int32, created time.Time) (Target, error) {
	row := s.db.QueryRow(
		`SELECT `+targetColumns+` FROM target WHERE pid = ? AND created = ?`,
		pid, created.UnixMilli())
	t, err := scanTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, fmt.Errorf("target for pid %d disappeared immediately after being stored", pid)
	}
	return t, err
}

// scanner is what *sql.Row and *sql.Rows have in common.
type scanner interface {
	Scan(dest ...any) error
}

func scanTarget(sc scanner) (Target, error) {
	var (
		t         Target
		createdMS int64
		addedMS   int64
		stoppedMS sql.NullInt64
		endedMS   sql.NullInt64
	)
	if err := sc.Scan(&t.ID, &t.PID, &createdMS, &t.Name, &t.Cmdline, &t.Cwd, &addedMS, &stoppedMS, &endedMS); err != nil {
		return Target{}, err
	}
	t.Created = time.UnixMilli(createdMS)
	t.AddedAt = time.UnixMilli(addedMS)
	t.StoppedAt = optionalTime(stoppedMS)
	t.EndedAt = optionalTime(endedMS)
	return t, nil
}

// optionalTime turns a nullable millisecond column into an optional time.
func optionalTime(ms sql.NullInt64) *time.Time {
	if !ms.Valid {
		return nil
	}
	t := time.UnixMilli(ms.Int64)
	return &t
}
