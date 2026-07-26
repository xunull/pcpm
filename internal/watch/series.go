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
	RSSBytes   int64
	Procs      int
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
	if bucket <= 0 {
		bucket = time.Second
	}
	// Reach one sampling interval before the window: a rate needs the reading
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

	type acc struct {
		cpuSeconds float64
		span       time.Duration
		rss        int64
		pids       map[int32]bool
		gap        bool
	}
	buckets := map[int64]*acc{}
	for _, d := range deltas {
		if d.at.Before(from) || !d.at.Before(to) {
			continue
		}
		key := d.at.UnixMilli() / bucket.Milliseconds()
		b := buckets[key]
		if b == nil {
			b = &acc{pids: map[int32]bool{}}
			buckets[key] = b
		}
		b.cpuSeconds += d.cpuSeconds
		b.span = max(b.span, d.span)
		b.rss += d.rss
		b.pids[d.pid] = true
		b.gap = b.gap || d.gap
	}

	out := make([]Point, 0, len(buckets))
	for key, b := range buckets {
		p := Point{
			At:       time.UnixMilli(key * bucket.Milliseconds()),
			RSSBytes: b.rss,
			Procs:    len(b.pids),
			Gap:      b.gap,
		}
		if b.span > 0 {
			p.CPUPercent = b.cpuSeconds / b.span.Seconds() * 100
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b Point) int { return a.At.Compare(b.At) })
	return out, nil
}

// lookback is how far before a window's start to reach for the reading a rate
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
	gap        bool
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
				span:       span,
				rss:        cur.RSSBytes,
				gap:        span > gapFactor*DefaultSampleInterval,
			})
		}
	}
	return out
}

// Summary reduces a target's history over a window to the figures a person asks
// for first, including which process is responsible — a tree figure has to stay
// decomposable into who produced it.
func (s *Store) Summary(targetID int64, from, to time.Time, bucket time.Duration) (Summary, error) {
	points, err := s.Series(targetID, from, to, bucket)
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
	}
	for _, p := range points {
		sum.PeakCPUPercent = max(sum.PeakCPUPercent, p.CPUPercent)
		sum.PeakRSSBytes = max(sum.PeakRSSBytes, p.RSSBytes)
	}
	if len(points) > 0 {
		last := points[len(points)-1]
		sum.CurrentCPUPercent = last.CPUPercent
		sum.CurrentRSSBytes = last.RSSBytes
	}
	sum.Processes = processBreakdown(samples)
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
	slices.SortFunc(out, func(a, b ProcessUsage) int {
		if a.CPUPercent != b.CPUPercent {
			if a.CPUPercent > b.CPUPercent {
				return -1
			}
			return 1
		}
		return int(a.PID - b.PID)
	})
	return out
}
