package watch

import (
	"database/sql"
	"errors"
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
func (s *Store) Rollup(to time.Time, bucket time.Duration) (int, error) {
	if bucket <= 0 {
		bucket = DefaultRollupInterval
	}
	from, err := s.rollupWatermark()
	if err != nil {
		return 0, err
	}
	// A bucket is only summarised once it can no longer gain samples.
	cutoff := to.Truncate(bucket)
	if !from.Before(cutoff) {
		return 0, nil
	}

	written, err := s.rollupRange(from, cutoff, bucket)
	if err != nil {
		return 0, err
	}
	if err := s.markRolledUpTo(cutoff); err != nil {
		return 0, err
	}
	return written, nil
}

// rollupAt summarises a range at a given resolution without touching the
// watermark, so a second resolution can be built alongside the primary one.
func (s *Store) rollupAt(from, to time.Time, bucket time.Duration) error {
	_, err := s.rollupRange(from, to, clampBucket(bucket))
	return err
}

// rollupRange writes the summaries for one range at one resolution.
func (s *Store) rollupRange(from, cutoff time.Time, bucket time.Duration) (int, error) {
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
		`INSERT INTO rollup (target_id, at, bucket_ms, pid, name, cpu_seconds, rss_bytes, span_ms, cpu_max, rss_max, net_in_bytes, net_out_bytes)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (target_id, at, bucket_ms, pid) DO UPDATE SET
		   cpu_seconds   = excluded.cpu_seconds,
		   rss_bytes     = excluded.rss_bytes,
		   span_ms       = excluded.span_ms,
		   cpu_max       = excluded.cpu_max,
		   rss_max       = excluded.rss_max,
		   net_in_bytes  = excluded.net_in_bytes,
		   net_out_bytes = excluded.net_out_bytes,
		   name          = excluded.name`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	written := 0
	for _, t := range targets {
		// Reach back a little so the first delta in the window has the sample
		// before it to subtract from.
		samples, err := s.SamplesBetween(t.ID, from.Add(-lookback), cutoff)
		if err != nil {
			return 0, err
		}
		for _, agg := range aggregate(cpuDeltas(samples), from, cutoff, bucket) {
			if _, err := stmt.Exec(t.ID, agg.at.UnixMilli(), bucket.Milliseconds(),
				agg.pid, agg.name, agg.cpuSeconds, agg.rssBytes, agg.span.Milliseconds(),
				agg.peak, agg.peakRSS, agg.traffic.InBytes, agg.traffic.OutBytes); err != nil {
				return 0, err
			}
			written++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// markRolledUpTo records how far summarising has got, so the next pass resumes
// rather than rescanning.
func (s *Store) markRolledUpTo(at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO meta (key, value) VALUES ('rollup_watermark', ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		at.UnixMilli())
	return err
}

// bucketed is one process's usage within one bucket, ready to store.
type bucketed struct {
	at         time.Time
	pid        int32
	name       string
	cpuSeconds float64
	rssBytes   int64
	traffic    Traffic
	span       time.Duration
	// peak is the highest rate any interval inside this bucket reached. It is
	// stored because the mean alone cannot be un-averaged later.
	peak    float64
	peakRSS int64
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
		b.traffic.InBytes += d.traffic.InBytes
		b.traffic.OutBytes += d.traffic.OutBytes
		b.span += d.span
		b.rssBytes = d.rss // the bucket's latest sample
		if d.span > 0 {
			b.peak = max(b.peak, d.cpuSeconds/d.span.Seconds()*100)
		}
		b.peakRSS = max(b.peakRSS, d.rss)
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
	bucket = clampBucket(bucket)
	rows, err := s.rolledRows(targetID, from, to)
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
			atMS, spanMS, rss, rssMax int64
			netIn, netOut             int64
			pid                       int32
			name                      string
			cpuSeconds, cpuMax        float64
		)
		if err := rows.Scan(&atMS, &pid, &name, &cpuSeconds, &rss, &spanMS, &cpuMax, &rssMax,
			&netIn, &netOut); err != nil {
			return nil, err
		}
		key := bucketKey{at: atMS / bucket.Milliseconds(), pid: pid}
		sh := acc[key]
		if sh == nil {
			sh = &share{}
			acc[key] = sh
		}
		sh.cpuSeconds += cpuSeconds
		sh.traffic.InBytes += netIn
		sh.traffic.OutBytes += netOut
		sh.span += time.Duration(spanMS) * time.Millisecond
		sh.rss = rss
		// Peaks survive re-aggregation by being carried, never averaged.
		sh.peak = max(sh.peak, cpuMax)
		sh.peakRSS = max(sh.peakRSS, rssMax)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildPoints(acc, nil, bucket), nil
}

// rolledRows reads the stored summaries covering a window.
//
// Only rows at the resolution they were written at are read: the table is keyed
// by bucket width so several can coexist, and mixing them would count the same
// period once per resolution.
func (s *Store) rolledRows(targetID int64, from, to time.Time) (*sql.Rows, error) {
	return s.db.Query(
		`SELECT at, pid, name, cpu_seconds, rss_bytes, span_ms, cpu_max, rss_max, net_in_bytes, net_out_bytes
		 FROM rollup
		 WHERE target_id = ? AND bucket_ms = ? AND at >= ? AND at < ?
		 ORDER BY at`,
		targetID, DefaultRollupInterval.Milliseconds(), from.UnixMilli(), to.UnixMilli())
}

// rolledBreakdown attributes a window's usage to individual processes using the
// stored summaries, for windows whose raw Samples have aged out.
func (s *Store) rolledBreakdown(targetID int64, from, to time.Time, bucket time.Duration) ([]ProcessUsage, error) {
	rows, err := s.rolledRows(targetID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type running struct {
		name       string
		cpuSeconds float64
		span       time.Duration
		rss        int64
	}
	totals := map[int32]*running{}
	for rows.Next() {
		var (
			atMS, spanMS, rss, rssMax int64
			netIn, netOut             int64
			pid                       int32
			name                      string
			cpuSeconds, cpuMax        float64
		)
		if err := rows.Scan(&atMS, &pid, &name, &cpuSeconds, &rss, &spanMS, &cpuMax, &rssMax,
			&netIn, &netOut); err != nil {
			return nil, err
		}
		_, _ = cpuMax, rssMax
		r := totals[pid]
		if r == nil {
			r = &running{}
			totals[pid] = r
		}
		r.name = name
		r.cpuSeconds += cpuSeconds
		r.span += time.Duration(spanMS) * time.Millisecond
		r.rss = rss
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ProcessUsage, 0, len(totals))
	for pid, r := range totals {
		u := ProcessUsage{PID: pid, Name: r.name, RSSBytes: r.rss}
		if r.span > 0 {
			u.CPUPercent = r.cpuSeconds / r.span.Seconds() * 100
		}
		out = append(out, u)
	}
	sortByBusiest(out)
	return out, nil
}

// SeriesFor answers a window from whichever resolution suits it. Callers ask
// for a window and a bucket; which table provides it is not their problem.
func (s *Store) SeriesFor(targetID int64, from, to time.Time, bucket time.Duration) ([]Point, error) {
	if to.Sub(from) <= rawWindowLimit {
		return s.Series(targetID, from, to, bucket)
	}
	rolled, err := s.RolledSeries(targetID, from, to, bucket)
	if err != nil {
		return nil, err
	}
	if len(rolled) > 0 {
		return rolled, nil
	}
	// Nothing has been summarised for this window yet — the collector rolls up
	// on a slow timer, and raw Samples are kept for longer than a day. Falling
	// back to them is what stops a target added ten minutes ago from showing an
	// empty chart on every window but the shortest.
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
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing has been rolled up yet.
		return time.Time{}, nil
	}
	if err != nil {
		// Anything else is a real failure. Treating it as "no watermark" would
		// silently turn every pass into a full rescan.
		return time.Time{}, err
	}
	return time.UnixMilli(ms), nil
}
