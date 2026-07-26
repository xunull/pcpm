package watch

import (
	"fmt"
	"time"
)

// Sample is one measurement of one process at one instant — never of a whole
// tree. Tree figures are derived from the samples beneath them, so that "which
// process was responsible" stays answerable afterwards.
//
// CPUSeconds is the process's *cumulative* CPU time, a counter that only ever
// climbs, not a percentage. Storing the counter rather than a rate keeps the
// averaging window a question for query time, and makes a gap between samples
// yield the true average across it rather than a spike at twice the real height
// (ADR-0008). RSSBytes has no such problem: memory is instantaneous by nature.
type Sample struct {
	At         time.Time
	PID        int32
	Created    time.Time // pins the process against PID reuse
	Name       string
	CPUSeconds float64
	RSSBytes   int64
}

// SaveSamples stores one tick's worth of measurements for a target. Re-saving a
// tick that is already stored replaces it rather than doubling the history, so
// a daemon that restarts mid-tick cannot corrupt what it has already written.
func (s *Store) SaveSamples(targetID int64, at time.Time, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO sample (target_id, at, pid, created, name, cpu_seconds, rss_bytes)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (target_id, at, pid) DO UPDATE SET
		   cpu_seconds = excluded.cpu_seconds,
		   rss_bytes   = excluded.rss_bytes,
		   name        = excluded.name`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range samples {
		if _, err := stmt.Exec(targetID, at.UnixMilli(), m.PID,
			m.Created.UnixMilli(), m.Name, m.CPUSeconds, m.RSSBytes); err != nil {
			return fmt.Errorf("storing sample for pid %d: %w", m.PID, err)
		}
	}
	return tx.Commit()
}

// SamplesBetween returns a target's samples in [from, to), oldest first. The
// half-open window means adjacent queries neither drop a sample nor count one
// twice.
func (s *Store) SamplesBetween(targetID int64, from, to time.Time) ([]Sample, error) {
	rows, err := s.db.Query(
		`SELECT at, pid, created, name, cpu_seconds, rss_bytes
		 FROM sample
		 WHERE target_id = ? AND at >= ? AND at < ?
		 ORDER BY at, pid`,
		targetID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var (
			m         Sample
			atMS      int64
			createdMS int64
		)
		if err := rows.Scan(&atMS, &m.PID, &createdMS, &m.Name, &m.CPUSeconds, &m.RSSBytes); err != nil {
			return nil, err
		}
		m.At = time.UnixMilli(atMS)
		m.Created = time.UnixMilli(createdMS)
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkEnded records that a target's last process has exited. Its samples stay:
// what it was doing before it died is usually the question worth asking.
func (s *Store) MarkEnded(targetID int64, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE target SET ended_at = ? WHERE id = ? AND ended_at IS NULL`,
		at.UnixMilli(), targetID)
	return err
}

// WatchedTargets returns the targets the collector should still measure: those
// the user has not stopped, whose processes have not all exited. This is what
// the daemon reads each tick — it takes its instructions from the database
// rather than from a control channel, so adding or stopping a target needs no
// IPC at all (ADR-0009).
func (s *Store) WatchedTargets() ([]Target, error) {
	rows, err := s.db.Query(
		`SELECT id, pid, created, name, cmdline, cwd, added_at, stopped_at, ended_at
		 FROM target
		 WHERE stopped_at IS NULL AND ended_at IS NULL
		 ORDER BY added_at, id`)
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
