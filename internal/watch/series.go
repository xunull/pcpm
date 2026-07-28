package watch

import (
	"slices"
	"sort"
	"time"
)

// Point is one bucket of a target's history: what the whole tree was doing over
// that slice of time.
type Point struct {
	At         time.Time
	CPUPercent float64 // 100 means one core saturated; a tree can exceed it
	// PeakCPUPercent is the highest rate seen inside this bucket. A bucket in a
	// week-long window covers hours, and a three-second request averages to
	// nothing across one — the peak is what keeps the evidence that something
	// still uses this process (ADR-0010).
	PeakCPUPercent float64
	PeakRSSBytes   int64
	RSSBytes       int64
	// Traffic is what moved during this bucket, not a cumulative counter, so
	// totals over a window are a sum of buckets rather than a difference
	// between endpoints (ADR-0012).
	Traffic Traffic
	Procs   int
	// Gap is true when the samples this bucket was derived from are further
	// apart than collection should have made them — the machine was asleep, or
	// the collector was not running. The figures remain correct averages; what
	// is missing is the shape within them, so a chart must draw a break rather
	// than imply a flat period.
	Gap bool
}

// ProcessUsage is one process's share of a target over a window.
type ProcessUsage struct {
	PID        int32
	Name       string
	CPUPercent float64
	RSSBytes   int64
}

// Summary is a target's history over a window, reduced to what a person asks
// first: is it busy now, how busy did it get, and which process is responsible.
type Summary struct {
	CurrentCPUPercent float64
	PeakCPUPercent    float64
	CurrentRSSBytes   int64
	PeakRSSBytes      int64
	Samples           int
	First             time.Time
	Last              time.Time
	Processes         []ProcessUsage // busiest first
}

// gapFactor is how many sampling intervals may pass between two samples before
// the span between them counts as a gap. Two is enough slack for a tick that
// merely ran late.
const gapFactor = 2

// Series returns a target's history over [from, to) in buckets of the given
// size. Window and bucket are the caller's choice, not this function's: the TUI
// asks for fixed windows and anything added later asks for its own, and both
// are the same query.
//
// CPU is derived here from the stored cumulative counters rather than read off
// a stored rate. That is what makes a gap between samples produce the true
// average across it instead of a spike, and what lets the same data answer at
// any bucket size (ADR-0008).
func (s *Store) Series(targetID int64, from, to time.Time, bucket time.Duration) ([]Point, error) {
	return s.series(targetID, 0, from, to, bucket)
}

// SeriesOfProcess is Series narrowed to one process in the tree, so a chart can
// show what a single member contributes against the whole. A pid of 0 means the
// whole tree.
func (s *Store) SeriesOfProcess(targetID int64, pid int32, from, to time.Time, bucket time.Duration) ([]Point, error) {
	return s.series(targetID, pid, from, to, bucket)
}

func (s *Store) series(targetID int64, only int32, from, to time.Time, bucket time.Duration) ([]Point, error) {
	bucket = clampBucket(bucket)
	// Reach one sampling interval before the window: a rate needs the sample
	// that precedes the first one inside it, or the window's opening bucket has
	// nothing to subtract from and silently disappears.
	samples, err := s.SamplesBetween(targetID, from.Add(-lookback), to)
	if err != nil {
		return nil, err
	}
	deltas := cpuDeltas(samples)
	if len(deltas) == 0 {
		return nil, nil
	}
	// A bucket finer than the collector's own cadence cannot be filled. Asking
	// for one leaves holes between the samples, and a hole means "nothing was
	// collected" — so a chart sized to a wide terminal would report a healthy
	// 30-second collector as mostly missing. The cadence comes from the data
	// because the interval is a configuration key.
	if cadence := medianSpan(deltas); cadence > bucket {
		bucket = cadence
	}

	acc := map[bucketKey]*share{}
	gaps := map[int64]bool{}
	for _, d := range deltas {
		if d.at.Before(from) || !d.at.Before(to) {
			continue
		}
		if only != 0 && d.pid != only {
			continue
		}
		key := bucketKey{at: d.at.UnixMilli() / bucket.Milliseconds(), pid: d.pid}
		sh := acc[key]
		if sh == nil {
			sh = &share{}
			acc[key] = sh
		}
		sh.cpuSeconds += d.cpuSeconds
		sh.traffic.InBytes += d.traffic.InBytes
		sh.traffic.OutBytes += d.traffic.OutBytes
		sh.span += d.span
		sh.rss = d.rss // the bucket's latest sample for this process
		if d.span > 0 {
			sh.peak = max(sh.peak, d.cpuSeconds/d.span.Seconds()*100)
		}
		sh.peakRSS = max(sh.peakRSS, d.rss)
		gaps[key.at] = gaps[key.at] || d.gap
	}
	return buildPoints(acc, gaps, bucket), nil
}

// clampBucket holds a bucket to something the stored resolution can honour.
// Times are kept in milliseconds, so a finer bucket cannot be delivered — and
// dividing by its zero millisecond count would panic.
func clampBucket(bucket time.Duration) time.Duration {
	if bucket < time.Millisecond {
		return time.Millisecond
	}
	return bucket
}

// bucketKey identifies one process's contribution to one bucket.
type bucketKey struct {
	at  int64 // bucket index
	pid int32
}

// share is what one process contributed to one bucket.
type share struct {
	cpuSeconds float64
	span       time.Duration
	rss        int64
	// traffic is bytes moved, which is additive — so a coarser bucket is built
	// by summing finer ones, and a window's total is a sum of its buckets.
	traffic Traffic
	// peak is the highest rate any single interval inside the bucket reached,
	// which averaging the bucket would otherwise erase.
	peak    float64
	peakRSS int64
}

// buildPoints folds per-process contributions into per-bucket points.
//
// A process's rate is its own CPU over its own elapsed time, and the tree's is
// the sum of those — not the tree's total CPU over the bucket's width. The
// distinction matters whenever a bucket is wider than the sampling interval,
// which every long window is: two processes each busy for half a bucket are not
// the same as one busy throughout, and dividing by a single span would silently
// multiply the answer by the number of samples in the bucket.
func buildPoints(acc map[bucketKey]*share, gaps map[int64]bool, bucket time.Duration) []Point {
	type total struct {
		cpuPercent float64
		peak       float64
		rss        int64
		peakRSS    int64
		traffic    Traffic
		procs      int
	}
	totals := map[int64]*total{}
	for key, sh := range acc {
		t := totals[key.at]
		if t == nil {
			t = &total{}
			totals[key.at] = t
		}
		if sh.span > 0 {
			t.cpuPercent += sh.cpuSeconds / sh.span.Seconds() * 100
		}
		// Peaks add across the tree for the same reason rates do: two processes
		// each briefly at 100% did use two cores.
		t.peak += sh.peak
		t.rss += sh.rss
		t.peakRSS += sh.peakRSS
		t.traffic.InBytes += sh.traffic.InBytes
		t.traffic.OutBytes += sh.traffic.OutBytes
		t.procs++
	}

	out := make([]Point, 0, len(totals))
	for at, t := range totals {
		out = append(out, Point{
			At:             time.UnixMilli(at * bucket.Milliseconds()),
			CPUPercent:     t.cpuPercent,
			PeakCPUPercent: max(t.peak, t.cpuPercent),
			RSSBytes:       t.rss,
			PeakRSSBytes:   max(t.peakRSS, t.rss),
			Traffic:        t.traffic,
			Procs:          t.procs,
			Gap:            gaps[at],
		})
	}
	slices.SortFunc(out, func(a, b Point) int { return a.At.Compare(b.At) })
	return out
}

// lookback is how far before a window's start to reach for the sample a rate
// needs to subtract from. It is generous on purpose: too short only costs the
// window's first bucket, and reading a few extra rows is cheap.
const lookback = 2 * time.Minute

// delta is one process's usage between two consecutive samples.
type delta struct {
	at         time.Time // the later of the two
	pid        int32
	cpuSeconds float64 // CPU consumed over the span
	span       time.Duration
	rss        int64 // resident memory at the later sample
	// traffic is what moved over the span. A counter that fell contributes
	// nothing rather than discarding the interval: for CPU a fall means a
	// different process, but for traffic it only means the source restarted.
	traffic Traffic
	gap     bool
}

// cpuDeltas turns cumulative counters into per-interval usage, one process at a
// time. A counter that goes backwards means the process was replaced, and its
// interval is dropped rather than subtracted into a negative rate.
func cpuDeltas(samples []Sample) []delta {
	byPID := map[int32][]Sample{}
	for _, m := range samples {
		byPID[m.PID] = append(byPID[m.PID], m)
	}

	var out []delta
	for pid, series := range byPID {
		sort.Slice(series, func(i, j int) bool { return series[i].At.Before(series[j].At) })
		for i := 1; i < len(series); i++ {
			prev, cur := series[i-1], series[i]
			span := cur.At.Sub(prev.At)
			if span <= 0 {
				continue
			}
			used := cur.CPUSeconds - prev.CPUSeconds
			if used < 0 {
				// The counter reset: this is a different process wearing the
				// same PID, or the same one restarted. There is no meaningful
				// rate across that boundary.
				continue
			}
			out = append(out, delta{
				at:         cur.At,
				pid:        pid,
				cpuSeconds: used,
				traffic: Traffic{
					InBytes:  rise(prev.Traffic.InBytes, cur.Traffic.InBytes),
					OutBytes: rise(prev.Traffic.OutBytes, cur.Traffic.OutBytes),
				},
				span: span,
				rss:  cur.RSSBytes,
			})
		}
	}
	markGaps(out)
	return out
}

// medianSpan is the typical interval between consecutive samples.
func medianSpan(deltas []delta) time.Duration {
	if len(deltas) == 0 {
		return 0
	}
	spans := make([]time.Duration, len(deltas))
	for i, d := range deltas {
		spans[i] = d.span
	}
	slices.Sort(spans)
	return spans[len(spans)/2]
}

// markGaps flags the intervals that are far longer than this data's own
// cadence.
//
// The threshold is taken from the samples rather than from the default
// interval, because the interval is a configuration key: a collector sampling
// every thirty seconds is not producing a gap every thirty seconds, and judging
// it against a compiled-in five would report one unbroken hole.
func markGaps(deltas []delta) {
	cadence := DefaultSampleInterval
	if len(deltas) >= 2 {
		if median := medianSpan(deltas); median > 0 {
			// The median, not the mean: a handful of real gaps must not drag
			// the baseline up to where they stop looking like gaps.
			cadence = median
		}
	}
	// Fewer than two intervals says nothing about cadence, so the default is
	// the only thing left to judge against. That is a fallback, not the rule —
	// using it when the data can speak for itself is what made a thirty-second
	// collector look like one unbroken hole.
	for i := range deltas {
		deltas[i].gap = deltas[i].span > gapFactor*cadence
	}
}

// Summary reduces a target's history over a window to the figures a person asks
// for first, including which process is responsible — a tree figure has to stay
// decomposable into who produced it.
func (s *Store) Summary(targetID int64, from, to time.Time, bucket time.Duration) (Summary, error) {
	points, err := s.SeriesFor(targetID, from, to, bucket)
	if err != nil {
		return Summary{}, err
	}
	samples, err := s.SamplesBetween(targetID, from, to)
	if err != nil {
		return Summary{}, err
	}

	sum := Summary{Samples: len(samples)}
	if len(samples) > 0 {
		sum.First, sum.Last = samples[0].At, samples[len(samples)-1].At
	} else if len(points) > 0 {
		// Raw samples have aged out but the rollups still cover the window, so
		// the summary reports what the charts are drawn from. Saying "no
		// samples" beside a chart showing 20%% would be worse than either.
		sum.Samples = len(points)
		sum.First, sum.Last = points[0].At, points[len(points)-1].At
	}
	for _, p := range points {
		sum.PeakCPUPercent = max(sum.PeakCPUPercent, p.PeakCPUPercent)
		sum.PeakRSSBytes = max(sum.PeakRSSBytes, p.PeakRSSBytes)
	}
	if len(points) > 0 {
		last := points[len(points)-1]
		sum.CurrentCPUPercent = last.CPUPercent
		sum.CurrentRSSBytes = last.RSSBytes
	}
	if len(samples) > 0 {
		sum.Processes = processBreakdown(samples)
	} else {
		sum.Processes, err = s.rolledBreakdown(targetID, from, to, bucket)
		if err != nil {
			return Summary{}, err
		}
	}
	return sum, nil
}

// processBreakdown attributes a window's usage to the individual processes,
// busiest first — the point of looking is to find what is doing the work.
func processBreakdown(samples []Sample) []ProcessUsage {
	type running struct {
		name       string
		cpuSeconds float64
		span       time.Duration
		rss        int64
	}
	totals := map[int32]*running{}
	for _, d := range cpuDeltas(samples) {
		r := totals[d.pid]
		if r == nil {
			r = &running{}
			totals[d.pid] = r
		}
		r.cpuSeconds += d.cpuSeconds
		r.span += d.span
		r.rss = d.rss
	}
	// Names come from the samples themselves: a process present for only one
	// sample has no delta but should still be nameable.
	for _, m := range samples {
		if r := totals[m.PID]; r != nil && r.name == "" {
			r.name = m.Name
		}
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
	return out
}

// sortByBusiest orders a breakdown with the heaviest process first: the point
// of looking at one is to find what is actually doing the work.
func sortByBusiest(usage []ProcessUsage) {
	slices.SortFunc(usage, func(a, b ProcessUsage) int {
		if a.CPUPercent != b.CPUPercent {
			if a.CPUPercent > b.CPUPercent {
				return -1
			}
			return 1
		}
		return int(a.PID - b.PID)
	})
}
