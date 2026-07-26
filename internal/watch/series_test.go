package watch

import (
	"math"
	"testing"
	"time"
)

func at(base time.Time, seconds int) time.Time {
	return base.Add(time.Duration(seconds) * time.Second)
}

// seed stores samples for one process: (offsetSeconds, cumulative CPU seconds).
func seed(t *testing.T, s *Store, targetID int64, base time.Time, pid int32, points [][2]float64) {
	t.Helper()
	for _, p := range points {
		when := base.Add(time.Duration(p[0] * float64(time.Second)))
		err := s.SaveSamples(targetID, when, []Sample{
			{PID: pid, Name: "proc", CPUSeconds: p[1], RSSBytes: 100 << 20},
		})
		if err != nil {
			t.Fatalf("SaveSamples: %v", err)
		}
	}
}

func seededTarget(t *testing.T) (*Store, int64, time.Time) {
	t.Helper()
	s := open(t)
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tgt, err := s.AddTarget(target(100, base.Add(-time.Hour)), base)
	if err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	return s, tgt.ID, base
}

func TestSeriesComputesCPURateFromTheCounter(t *testing.T) {
	s, id, base := seededTarget(t)
	// 5s apart, 1 CPU-second each interval: a steady 20%
	seed(t, s, id, base, 100, [][2]float64{{0, 10}, {5, 11}, {10, 12}, {15, 13}})

	got, err := s.Series(id, base, at(base, 20), 5*time.Second)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 buckets (the first sample only establishes the baseline), got %d", len(got))
	}
	for _, b := range got {
		if math.Abs(b.CPUPercent-20) > 0.01 {
			t.Errorf("bucket at %v: CPU %.2f%%, want 20%%", b.At, b.CPUPercent)
		}
	}
}

// The point of storing a counter rather than a rate: a gap must yield the true
// average across it, not a spike at several times the real height (ADR-0008).
func TestSeriesSpreadsUsageAcrossAGap(t *testing.T) {
	s, id, base := seededTarget(t)
	// The machine slept: 60s passed between samples, during which 6 CPU-seconds
	// were used. That is 10%, not 120%.
	seed(t, s, id, base, 100, [][2]float64{{0, 10}, {60, 16}})

	got, err := s.Series(id, base, at(base, 120), time.Minute)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(got))
	}
	if math.Abs(got[0].CPUPercent-10) > 0.01 {
		t.Errorf("CPU across a 60s gap = %.2f%%, want 10%% — a rate stored at collection time would have shown 120%%", got[0].CPUPercent)
	}
	if !got[0].Gap {
		t.Error("a bucket spanning a gap should say so, or the chart implies the period was flat")
	}
}

// A process replaced under the same target restarts its counter. Subtracting
// blind would give a large negative rate.
func TestSeriesIgnoresACounterReset(t *testing.T) {
	s, id, base := seededTarget(t)
	seed(t, s, id, base, 100, [][2]float64{{0, 500}, {5, 501}, {10, 2}, {15, 3}})

	got, err := s.Series(id, base, at(base, 20), 5*time.Second)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	for _, b := range got {
		if b.CPUPercent < 0 {
			t.Errorf("bucket at %v has a negative CPU rate (%.2f%%); a counter reset was subtracted blind", b.At, b.CPUPercent)
		}
		if b.CPUPercent > 100 {
			t.Errorf("bucket at %v reports %.2f%%, which a single process cannot use", b.At, b.CPUPercent)
		}
	}
}

// A tree's figure is the sum of its processes at that moment, derived at read
// time — never stored pre-aggregated.
func TestSeriesSumsTheTree(t *testing.T) {
	s, id, base := seededTarget(t)
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 1}})   // 20%
	seed(t, s, id, base, 101, [][2]float64{{0, 0}, {5, 0.5}}) // 10%

	got, err := s.Series(id, base, at(base, 10), 5*time.Second)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(got))
	}
	if math.Abs(got[0].CPUPercent-30) > 0.01 {
		t.Errorf("tree CPU = %.2f%%, want 30%% (20 + 10)", got[0].CPUPercent)
	}
	if got[0].RSSBytes != 200<<20 {
		t.Errorf("tree RSS = %d, want the sum of both processes", got[0].RSSBytes)
	}
	if got[0].Procs != 2 {
		t.Errorf("Procs = %d, want 2", got[0].Procs)
	}
}

// The window and bucket size are the caller's to choose: the TUI's fixed
// windows and any later UI are both just callers of the same query.
func TestSeriesHonoursTheRequestedBucket(t *testing.T) {
	s, id, base := seededTarget(t)
	var points [][2]float64
	for i := range 13 {
		points = append(points, [2]float64{float64(i * 5), float64(i)})
	}
	seed(t, s, id, base, 100, points)

	fine, err := s.Series(id, base, at(base, 60), 5*time.Second)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	coarse, err := s.Series(id, base, at(base, 60), 30*time.Second)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}

	if len(fine) <= len(coarse) {
		t.Errorf("a 5s bucket should give more points than a 30s one: %d vs %d", len(fine), len(coarse))
	}
	if len(coarse) != 2 {
		t.Errorf("60s at 30s buckets should be 2 points, got %d", len(coarse))
	}
}

func TestSeriesOfATargetWithNoSamples(t *testing.T) {
	s, id, base := seededTarget(t)

	got, err := s.Series(id, base, at(base, 60), 5*time.Second)
	if err != nil {
		t.Fatalf("an empty target should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want no points, got %d", len(got))
	}
}

func TestSummaryReportsCurrentAndPeak(t *testing.T) {
	s, id, base := seededTarget(t)
	// steady 20%, then a burst to 100%, then back down
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 1}, {10, 6}, {15, 7}})

	sum, err := s.Summary(id, base, at(base, 20), 5*time.Second)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if math.Abs(sum.PeakCPUPercent-100) > 0.01 {
		t.Errorf("peak CPU = %.2f%%, want 100%%", sum.PeakCPUPercent)
	}
	if math.Abs(sum.CurrentCPUPercent-20) > 0.01 {
		t.Errorf("current CPU = %.2f%%, want the latest bucket's 20%%", sum.CurrentCPUPercent)
	}
	if sum.Samples != 4 {
		t.Errorf("Samples = %d, want 4", sum.Samples)
	}
	if sum.First.IsZero() || sum.Last.IsZero() {
		t.Error("the covered period should be reported")
	}
	if len(sum.Processes) != 1 || sum.Processes[0].PID != 100 {
		t.Errorf("want a per-process breakdown, got %+v", sum.Processes)
	}
}

// A tree figure has to stay decomposable into who was responsible for it.
func TestSummaryBreaksDownByProcess(t *testing.T) {
	s, id, base := seededTarget(t)
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 0.1}}) // the wrapper: 2%
	seed(t, s, id, base, 101, [][2]float64{{0, 0}, {5, 2}})   // the worker: 40%

	sum, err := s.Summary(id, base, at(base, 10), 5*time.Second)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(sum.Processes) != 2 {
		t.Fatalf("want both processes, got %d", len(sum.Processes))
	}
	// busiest first: the point is to find what is actually doing the work
	if sum.Processes[0].PID != 101 {
		t.Errorf("want the busiest process first, got pid %d", sum.Processes[0].PID)
	}
	if math.Abs(sum.Processes[0].CPUPercent-40) > 0.01 {
		t.Errorf("worker CPU = %.2f%%, want 40%%", sum.Processes[0].CPUPercent)
	}
}

// A bucket wider than the sampling interval holds several readings per process.
// Their elapsed times must add up, not be collapsed to the longest: dividing a
// bucket's total CPU by one interval's span multiplies the answer by however
// many samples landed in it. Every long window uses coarse buckets, so this is
// the normal case, not an edge one.
func TestSeriesIsCorrectWhenABucketHoldsManySamples(t *testing.T) {
	s, id, base := seededTarget(t)
	// two minutes of a steady 20%: 1 CPU-second per 5s interval
	var points [][2]float64
	for i := range 25 {
		points = append(points, [2]float64{float64(i * 5), float64(i)})
	}
	seed(t, s, id, base, 100, points)

	fine, err := s.Series(id, base, at(base, 120), 5*time.Second)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	coarse, err := s.Series(id, base, at(base, 120), time.Minute)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}

	for _, b := range fine {
		if math.Abs(b.CPUPercent-20) > 0.01 {
			t.Fatalf("5s bucket at %v: %.2f%%, want 20%%", b.At, b.CPUPercent)
		}
	}
	// The same steady load must read the same at any resolution.
	for _, b := range coarse {
		if math.Abs(b.CPUPercent-20) > 0.5 {
			t.Errorf("60s bucket at %v: %.2f%%, want 20%% — twelve 5s readings were treated as one",
				b.At, b.CPUPercent)
		}
	}
}

// Two processes each busy for half a bucket are not one process busy
// throughout, so each contributes its own rate rather than sharing one span.
func TestSeriesAddsEachProcessOwnRate(t *testing.T) {
	s, id, base := seededTarget(t)
	// pid 100 busy in the first half of the minute, pid 101 in the second
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 5}})   // 100%
	seed(t, s, id, base, 101, [][2]float64{{30, 0}, {35, 5}}) // 100%

	got, err := s.Series(id, base, at(base, 60), time.Minute)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(got))
	}
	if math.Abs(got[0].CPUPercent-200) > 0.01 {
		t.Errorf("CPU = %.2f%%, want 200%% (two processes at 100%% each)", got[0].CPUPercent)
	}
}
