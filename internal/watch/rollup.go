package watch

import (
	"database/sql"
	"slices"
	"time"
)

// Defaults for how long each resolution is kept. Raw Samples answer "what
// exactly happened yesterday afternoon" and are the expensive ones; rollups
// answer "has this been creeping up for a fortnight" at 1/59th the rows.
const (
	DefaultMaintenanceInterval = 5 * time.Minute
	DefaultRollupInterval      = time.Minute
	DefaultRawRetention        = 48 * time.Hour
	DefaultRollupRetention     = 30 * 24 * time.Hour
)

// rawWindowLimit is how long a window may be before it is answered from
// rollups instead of raw Samples. Measured on a 10M-row table, a query
// spanning 11.6 days took 7.2s against raw samples and 135ms against
// one-minute rollups (ADR-0007).
const rawWindowLimit = 6 * time.Hour

// Rollup summarises raw Samples into per-bucket rows, returning how many
// buckets it wrote. It is incremental: each pass starts where the last one
// finished, so running it on a timer costs the same whether the database holds
// an hour of history or a month.
//
// The rows carry the per-bucket **delta** of the CPU counter, not an average of
// the counter. Averaging a counter and then differencing it gives a number with
// no meaning; summing the deltas within a bucket gives exactly the CPU consumed
// during it (ADR-0008).
func (s *Store) Rollup(from, to time.Time, bucket time.Duration) (int, error) {
	if bucket <= 0 {
		bucket = DefaultRollupInterval
	}
	watermark, err := s.rollupWatermark()
	if err != nil {
		return 0, err
	}
	if watermark.After(from) {
		from = watermark
	}
	// A bucket is only summarised once it can no longer gain samples.
	cutoff := to.Truncate(bucket)
	if !from.Before(cutoff) {
		return 0, nil
	}

	targets, err := s.Targets()
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO rollup (target_id, at, bucket_ms, pid, name, cpu_seconds, rss_bytes, span_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (target_id, at, bucket_ms, pid) DO UPDATE SET
		   cpu_seconds = excluded.cpu_seconds,
		   rss_bytes   = excluded.rss_bytes,
		   span_ms     = excluded.span_ms,
		   name        = excluded.name`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	written := 0
	for _, t := range targets {
		// Reach back a little so the first delta in the window has the reading
		// before it to subtract from.
		samples, err := s.SamplesBetween(t.ID, from.Add(-lookback), cutoff)
		if err != nil {
			return 0, err
		}
		for _, agg := range aggregate(cpuDeltas(samples), from, cutoff, bucket) {
			if _, err := stmt.Exec(t.ID, agg.at.UnixMilli(), bucket.Milliseconds(),
				agg.pid, agg.name, agg.cpuSeconds, agg.rssBytes, agg.span.Milliseconds()); err != nil {
				return 0, err
			}
			written++
		}
	}
	if err := s.setRollupWatermark(tx, cutoff); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// bucketed is one process's usage within one bucket, ready to store.
type bucketed struct {
	at         time.Time
	pid        int32
	name       string
	cpuSeconds float64
	rssBytes   int64
	span       time.Duration
}

// aggregate folds per-interval deltas into per-bucket totals, one process at a
// time. Summing cpuSeconds and span separately is what lets a rate be recovered
// later at any coarser bucket: both are additive, their ratio is not.
func aggregate(deltas []delta, from, to time.Time, bucket time.Duration) []bucketed {
	type key struct {
		bucket int64
		pid    int32
	}
	acc := map[key]*bucketed{}
	for _, d := range deltas {
		if d.at.Before(from) || !d.at.Before(to) {
			continue
		}
		k := key{d.at.UnixMilli() / bucket.Milliseconds(), d.pid}
		b := acc[k]
		if b == nil {
			b = &bucketed{at: time.UnixMilli(k.bucket * bucket.Milliseconds()), pid: d.pid}
			acc[k] = b
		}
		b.cpuSeconds += d.cpuSeconds
		b.span += d.span
		b.rssBytes = d.rss // the bucket's latest reading
	}

	out := make([]bucketed, 0, len(acc))
	for _, b := range acc {
		out = append(out, *b)
	}
	slices.SortFunc(out, func(a, b bucketed) int { return a.at.Compare(b.at) })
	return out
}

// RolledSeries answers the same question as Series, from the rollup table.
func (s *Store) RolledSeries(targetID int64, from, to time.Time, bucket time.Duration) ([]Point, error) {
	if bucket <= 0 {
		bucket = DefaultRollupInterval
	}
	rows, err := s.db.Query(
		`SELECT at, pid, cpu_seconds, rss_bytes, span_ms
		 FROM rollup
		 WHERE target_id = ? AND at >= ? AND at < ?
		 ORDER BY at`,
		targetID, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Rolled rows are already per (bucket, process), and both cpu_seconds and
	// span_ms are additive — which is what lets a coarser bucket be built by
	// summing finer ones. Folding them into points is then the same operation
	// the raw path performs.
	acc := map[bucketKey]*share{}
	for rows.Next() {
		var (
			atMS, spanMS, rss int64
			pid               int32
			cpuSeconds        float64
		)
		if err := rows.Scan(&atMS, &pid, &cpuSeconds, &rss, &spanMS); err != nil {
			return nil, err
		}
		key := bucketKey{at: atMS / bucket.Milliseconds(), pid: pid}
		sh := acc[key]
		if sh == nil {
			sh = &share{}
			acc[key] = sh
		}
		sh.cpuSeconds += cpuSeconds
		sh.span += time.Duration(spanMS) * time.Millisecond
		sh.rss = rss
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildPoints(acc, nil, bucket), nil
}

// SeriesFor answers a window from whichever resolution suits it. Callers ask
// for a window and a bucket; which table provides it is not their problem.
func (s *Store) SeriesFor(targetID int64, from, to time.Time, bucket time.Duration) ([]Point, error) {
	if to.Sub(from) > rawWindowLimit {
		return s.RolledSeries(targetID, from, to, bucket)
	}
	return s.Series(targetID, from, to, bucket)
}

// Retain drops what has aged out: raw Samples past rawFor, rollups past
// rollupFor. It returns how many rows went.
//
// Raw samples go first and are the bulk of the database; the rollups that
// outlive them are what keeps a month of history answerable at 1/59th the rows.
func (s *Store) Retain(now time.Time, rawFor, rollupFor time.Duration) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	total := int64(0)
	res, err := tx.Exec(`DELETE FROM sample WHERE at < ?`, now.Add(-rawFor).UnixMilli())
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err == nil {
		total += n
	}

	res, err = tx.Exec(`DELETE FROM rollup WHERE at < ?`, now.Add(-rollupFor).UnixMilli())
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err == nil {
		total += n
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(total), nil
}

// rollupWatermark is the point up to which samples have already been
// summarised. Keeping it makes each pass incremental instead of a full rescan.
func (s *Store) rollupWatermark() (time.Time, error) {
	var ms int64
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'rollup_watermark'`).Scan(&ms)
	if err != nil {
		// No watermark yet: nothing has been rolled up.
		return time.Time{}, nil
	}
	return time.UnixMilli(ms), nil
}

func (s *Store) setRollupWatermark(tx *sql.Tx, at time.Time) error {
	_, err := tx.Exec(
		`INSERT INTO meta (key, value) VALUES ('rollup_watermark', ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		at.UnixMilli())
	return err
}
