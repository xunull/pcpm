package watch

import (
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/proc"
)

// After retention drops raw Samples, a long window is still answerable from
// rollups — so the summary must come from the same place the charts do.
// Reporting "no samples" beside a chart showing 20% is worse than either.
func TestSummaryFallsBackToRollupsAfterRetention(t *testing.T) {
	s, id, base := seededTarget(t)
	old := base.Add(-72 * time.Hour)
	seed(t, s, id, old, 100, [][2]float64{{0, 0}, {5, 1}, {10, 2}})
	seed(t, s, id, old, 101, [][2]float64{{0, 0}, {5, 3}, {10, 6}})
	if _, err := s.Rollup(base, time.Minute); err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if _, err := s.Retain(base, 48*time.Hour, 720*time.Hour); err != nil {
		t.Fatalf("Retain: %v", err)
	}

	sum, err := s.Summary(id, base.Add(-7*24*time.Hour), base, time.Hour)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.Samples == 0 {
		t.Error("Samples is 0 while the charts have data; the summary ignored the rollups")
	}
	if len(sum.Processes) != 2 {
		t.Errorf("want both processes in the breakdown, got %d", len(sum.Processes))
	}
	if sum.First.IsZero() || sum.Last.IsZero() {
		t.Error("the covered period should still be reported")
	}
}

// A gap means "further apart than this collector was configured to sample",
// which cannot be judged against a compiled-in constant: the interval is a
// config key. A steady cadence must never read as a gap, whatever it is.
func TestGapIsRelativeToTheActualCadence(t *testing.T) {
	for _, cadence := range []time.Duration{time.Second, 30 * time.Second, 2 * time.Minute} {
		s, id, base := seededTarget(t)
		for i := range 10 {
			seed(t, s, id, base, 100, [][2]float64{{cadence.Seconds() * float64(i), float64(i)}})
		}

		got, err := s.Series(id, base, base.Add(cadence*12), cadence*2)
		if err != nil {
			t.Fatalf("Series: %v", err)
		}
		for _, p := range got {
			if p.Gap {
				t.Errorf("a steady %s cadence was reported as a gap", cadence)
				break
			}
		}
	}
}

// A genuine break must still be caught.
func TestGapIsStillDetectedWhenCollectionStops(t *testing.T) {
	s, id, base := seededTarget(t)
	for i := range 5 {
		seed(t, s, id, base, 100, [][2]float64{{float64(i * 5), float64(i)}})
	}
	// nothing for ten minutes, then it resumes
	seed(t, s, id, base, 100, [][2]float64{{620, 10}, {625, 11}})

	got, err := s.Series(id, base, base.Add(15*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}
	found := false
	for _, p := range got {
		if p.Gap {
			found = true
		}
	}
	if !found {
		t.Error("ten minutes with nothing collected should be reported as a gap")
	}
}

// A bucket smaller than the millisecond the times are stored in cannot be
// honoured; it must be clamped, not divided by.
func TestSeriesSurvivesASubMillisecondBucket(t *testing.T) {
	s, id, base := seededTarget(t)
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 1}})

	if _, err := s.Series(id, base, base.Add(time.Minute), 100*time.Microsecond); err != nil {
		t.Fatalf("a sub-millisecond bucket should be clamped, not fail: %v", err)
	}
	if _, err := s.RolledSeries(id, base, base.Add(time.Minute), 100*time.Microsecond); err != nil {
		t.Fatalf("RolledSeries: %v", err)
	}
}

// Rollup rows are keyed by bucket width so several resolutions can coexist.
// A query that ignores the width would sum them together and double-count.
func TestRolledSeriesReadsOnlyItsOwnResolution(t *testing.T) {
	s, id, base := seededTarget(t)
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 1}, {10, 2}, {15, 3}})
	if _, err := s.Rollup(base.Add(time.Minute), time.Minute); err != nil {
		t.Fatalf("Rollup at 1m: %v", err)
	}
	oneMinute, err := s.RolledSeries(id, base, base.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("RolledSeries: %v", err)
	}
	if len(oneMinute) == 0 {
		t.Fatal("no rollup rows to compare against")
	}
	before := oneMinute[0].CPUPercent

	// A second resolution lands in the same table.
	if err := s.rollupAt(base, base.Add(time.Minute), 10*time.Second); err != nil {
		t.Fatalf("second resolution: %v", err)
	}
	after, err := s.RolledSeries(id, base, base.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("RolledSeries: %v", err)
	}
	if after[0].CPUPercent != before {
		t.Errorf("a second resolution changed the 1m answer: %.2f%% -> %.2f%%", before, after[0].CPUPercent)
	}
}

// A target ends when its processes have ALL exited. A root that dies leaving
// children behind has not ended — those children are exactly what this tool
// exists to notice.
func TestTargetSurvivesItsRootWhenChildrenLiveOn(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{
			{PID: 100, PPID: 1, Name: "wrapper", Created: started},
			{PID: 101, PPID: 100, Name: "server", Created: started},
		},
		cpu: map[int32]float64{100: 1, 101: 5},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: started, Name: "wrapper"}, *clock)
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// the wrapper exits; the server it started keeps running
	m.procs = []proc.Process{{PID: 101, PPID: 1, Name: "server", Created: started}}
	*clock = clock.Add(31 * time.Second)
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	targets, _ := s.Targets()
	if targets[0].Ended() {
		t.Error("the target was marked ended while one of its processes is still running")
	}
	got, _ := s.SamplesBetween(tgt.ID, *clock, clock.Add(time.Second))
	if len(got) != 1 || got[0].PID != 101 {
		t.Errorf("the surviving child should still be sampled, got %+v", got)
	}
}

// A chart sized to a wide terminal asks for buckets finer than the collector's
// interval. Honouring that literally scatters the samples into every other
// bucket and leaves the rest empty — and empty means "nothing was collected".
func TestSeriesWillNotProduceBucketsFinerThanTheData(t *testing.T) {
	s, id, base := seededTarget(t)
	// a 30-second collector, which is a supported configuration
	for i := range 120 {
		seed(t, s, id, base, 100, [][2]float64{{float64(i * 30), float64(i) * 9}})
	}

	// ask for 22.5-second buckets, as an 80-column chart of an hour would
	got, err := s.Series(id, base, base.Add(time.Hour), 22500*time.Millisecond)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}

	// Consecutive points must be one cadence apart, not scattered.
	for i := 1; i < len(got); i++ {
		if step := got[i].At.Sub(got[i-1].At); step < 30*time.Second {
			t.Fatalf("points are %s apart for a 30s collector: the bucket was finer than the data", step)
		}
	}
	if len(got) < 100 {
		t.Errorf("want roughly one point per sample, got %d from 120 samples", len(got))
	}
}

// Rollups are written on a slow timer while raw Samples are kept for two days,
// so a long window is answerable from raw data long before it is summarised.
// Returning nothing would show an empty chart for a target added minutes ago.
func TestLongWindowFallsBackToRawSamplesBeforeAnythingIsRolledUp(t *testing.T) {
	s, id, base := seededTarget(t)
	start := base.Add(-24 * time.Hour)
	for i := range 200 {
		seed(t, s, id, start, 100, [][2]float64{{float64(i * 300), float64(i)}})
	}
	// deliberately no Rollup call

	got, err := s.SeriesFor(id, start, base, 10*time.Minute)
	if err != nil {
		t.Fatalf("SeriesFor: %v", err)
	}
	if len(got) == 0 {
		t.Error("a 24-hour window returned nothing while raw samples covering it are still stored")
	}
}
