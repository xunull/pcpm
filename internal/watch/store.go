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
const schemaVersion = 1

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

// Close releases the database.
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
		 ON CONFLICT (pid, created) DO UPDATE SET stopped_at = NULL`,
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

// Targets returns every target, watched or stopped, oldest first.
func (s *Store) Targets() ([]Target, error) {
	rows, err := s.db.Query(
		`SELECT id, pid, created, name, cmdline, cwd, added_at, stopped_at
		 FROM target ORDER BY added_at, id`)
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
		`SELECT id, pid, created, name, cmdline, cwd, added_at, stopped_at
		 FROM target WHERE pid = ? AND created = ?`, pid, created.UnixMilli())
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
	)
	if err := sc.Scan(&t.ID, &t.PID, &createdMS, &t.Name, &t.Cmdline, &t.Cwd, &addedMS, &stoppedMS); err != nil {
		return Target{}, err
	}
	t.Created = time.UnixMilli(createdMS)
	t.AddedAt = time.UnixMilli(addedMS)
	if stoppedMS.Valid {
		stopped := time.UnixMilli(stoppedMS.Int64)
		t.StoppedAt = &stopped
	}
	return t, nil
}
