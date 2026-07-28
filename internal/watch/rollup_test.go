package watch

import (
	"math"
	"testing"
	"time"
)

// The whole point of rolling up: the same question must get the same answer,
// whether it is computed from raw Samples or from the summaries that replaced
// them. Averaging the counter instead of summing its per-bucket delta is the
// mistake this catches.
func TestRollupPreservesTheRate(t *testing.T) {
	s, id, base := seededTarget(t)
	// two minutes at 5s, a steady 20% (1 CPU-second per 5s interval)
	var points [][2]float64
	for i := range 25 {
		points = append(points, [2]float64{float64(i * 5), float64(i)})
	}
	seed(t, s, id, base, 100, points)

	fromRaw, err := s.Series(id, base, at(base, 120), time.Minute)
	if err != nil {
		t.Fatalf("Series: %v", err)
	}

	if _, err := s.Rollup(at(base, 300), time.Minute); err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	fromRollup, err := s.RolledSeries(id, base, at(base, 120), time.Minute)
	if err != nil {
		t.Fatalf("RolledSeries: %v", err)
	}

	if len(fromRollup) != len(fromRaw) {
		t.Fatalf("rollup has %d buckets, raw has %d", len(fromRollup), len(fromRaw))
	}
	for i := range fromRaw {
		if math.Abs(fromRaw[i].CPUPercent-fromRollup[i].CPUPercent) > 0.5 {
			t.Errorf("bucket %d: raw says %.2f%%, rollup says %.2f%% — the delta was not preserved",
				i, fromRaw[i].CPUPercent, fromRollup[i].CPUPercent)
		}
	}
}

func TestRollupIsIncremental(t *testing.T) {
	s, id, base := seededTarget(t)
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 1}, {10, 2}})

	first, err := s.Rollup(at(base, 60), time.Minute)
	if err != nil {
		t.Fatalf("first Rollup: %v", err)
	}
	if first == 0 {
		t.Fatal("the first rollup summarised nothing")
	}

	// Nothing new has been collected, so a second pass has nothing to do rather
	// than re-summarising everything.
	again, err := s.Rollup(at(base, 60), time.Minute)
	if err != nil {
		t.Fatalf("second Rollup: %v", err)
	}
	if again != 0 {
		t.Errorf("the second pass re-summarised %d buckets; want it to pick up where it left off", again)
	}

	// New samples do get picked up.
	seed(t, s, id, base, 100, [][2]float64{{65, 3}, {70, 4}})
	third, err := s.Rollup(at(base, 120), time.Minute)
	if err != nil {
		t.Fatalf("third Rollup: %v", err)
	}
	if third == 0 {
		t.Error("new samples were not rolled up")
	}
}

func TestRetentionDropsRawSamplesButKeepsRollups(t *testing.T) {
	s, id, base := seededTarget(t)
	old := base.Add(-72 * time.Hour)
	seed(t, s, id, old, 100, [][2]float64{{0, 0}, {5, 1}, {10, 2}})
	seed(t, s, id, base, 100, [][2]float64{{0, 100}, {5, 101}})

	if _, err := s.Rollup(at(base, 60), time.Minute); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	// keep 48h of raw samples, 30 days of rollups
	dropped, err := s.Retain(base, 48*time.Hour, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if dropped == 0 {
		t.Error("nothing was dropped, but there are samples older than the window")
	}

	stillRaw, _ := s.SamplesBetween(id, old.Add(-time.Hour), base.Add(time.Hour))
	for _, m := range stillRaw {
		if m.At.Before(base.Add(-48 * time.Hour)) {
			t.Errorf("a raw sample from %v outlived the 48h window", m.At)
		}
	}
	if len(stillRaw) == 0 {
		t.Error("retention removed the recent samples too")
	}

	// the rolled-up history of that old period survives
	rolled, err := s.RolledSeries(id, old.Add(-time.Hour), old.Add(time.Hour), time.Minute)
	if err != nil {
		t.Fatalf("RolledSeries: %v", err)
	}
	if len(rolled) == 0 {
		t.Error("the old period is gone entirely; the rollup should have outlived the raw samples")
	}
}

func TestRetainKeepsRollupsWithinTheirOwnWindow(t *testing.T) {
	s, id, base := seededTarget(t)
	ancient := base.Add(-60 * 24 * time.Hour)
	seed(t, s, id, ancient, 100, [][2]float64{{0, 0}, {5, 1}})
	if _, err := s.Rollup(base, time.Minute); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	if _, err := s.Retain(base, 48*time.Hour, 30*24*time.Hour); err != nil {
		t.Fatalf("Retain: %v", err)
	}

	rolled, _ := s.RolledSeries(id, ancient.Add(-time.Hour), ancient.Add(time.Hour), time.Minute)
	if len(rolled) != 0 {
		t.Errorf("a 60-day-old rollup outlived the 30-day window: %d buckets", len(rolled))
	}
}

// Callers ask for a window; which table answers is not their problem.
func TestSeriesForChoosesTheTableFromTheWindow(t *testing.T) {
	s, id, base := seededTarget(t)
	seed(t, s, id, base, 100, [][2]float64{{0, 0}, {5, 1}, {10, 2}, {15, 3}})
	if _, err := s.Rollup(at(base, 60), time.Minute); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	// A short window is answerable from raw samples.
	short, err := s.SeriesFor(id, base, at(base, 20), 5*time.Second)
	if err != nil {
		t.Fatalf("SeriesFor short: %v", err)
	}
	if len(short) == 0 {
		t.Error("a short window returned nothing")
	}

	// A long one is not: at minute buckets there is nothing finer to gain, and
	// scanning days of raw samples is what the rollup exists to avoid.
	long, err := s.SeriesFor(id, base.Add(-20*24*time.Hour), at(base, 60), time.Hour)
	if err != nil {
		t.Fatalf("SeriesFor long: %v", err)
	}
	if len(long) == 0 {
		t.Error("a long window returned nothing; the rollup was not consulted")
	}
}

// seedTraffic stores samples carrying a cumulative traffic counter alongside CPU.
func seedTraffic(t *testing.T, s *Store, targetID int64, base time.Time, pid int32, counters []int64) {
	t.Helper()
	for i, c := range counters {
		when := base.Add(time.Duration(i*5) * time.Second)
		err := s.SaveSamples(targetID, when, []Sample{{
			PID: pid, Name: "proc", CPUSeconds: float64(i), RSSBytes: 100 << 20,
			Traffic: Traffic{InBytes: c, OutBytes: c / 2},
		}})
		if err != nil {
			t.Fatalf("SaveSamples: %v", err)
		}
	}
}

// Raw Samples are dropped after their retention period. Without traffic in the
// rollups the chart would empty from the left two days later while the CPU
// chart beside it stayed full — a failure whose cause is two days upstream of
// its symptom.
func TestTrafficSurvivesTheRollup(t *testing.T) {
	s, id, base := seededTarget(t)
	// 1 MiB per 5s interval for two minutes
	var counters []int64
	for i := range 25 {
		counters = append(counters, int64(i)<<20)
	}
	seedTraffic(t, s, id, base, 100, counters)

	fromRaw, err := s.Series(id, base, at(base, 120), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rollup(at(base, 300), time.Minute); err != nil {
		t.Fatal(err)
	}
	fromRollup, err := s.RolledSeries(id, base, at(base, 120), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	sum := func(ps []Point) (int64, int64) {
		var in, out int64
		for _, p := range ps {
			in += p.Traffic.InBytes
			out += p.Traffic.OutBytes
		}
		return in, out
	}
	rawIn, rawOut := sum(fromRaw)
	rollIn, rollOut := sum(fromRollup)

	if rawIn == 0 {
		t.Fatal("the raw series carries no traffic to compare against")
	}
	if rollIn != rawIn || rollOut != rawOut {
		t.Errorf("rollup totals %d/%d, raw totals %d/%d — traffic did not survive",
			rollIn, rollOut, rawIn, rawOut)
	}
}

// A total over a window is a sum of buckets, so it stays correct when the
// counter behind it began again — which happens every time the collector
// restarts (ADR-0012).
func TestATotalIsUnaffectedByACounterThatRestarted(t *testing.T) {
	s, id, base := seededTarget(t)
	// climbs to 4 MiB, the collector restarts, climbs to 3 MiB again
	seedTraffic(t, s, id, base, 100, []int64{
		0, 1 << 20, 2 << 20, 3 << 20, 4 << 20,
		0, 1 << 20, 2 << 20, 3 << 20,
	})

	points, err := s.Series(id, base, at(base, 300), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, p := range points {
		total += p.Traffic.InBytes
	}

	// 4 MiB before the restart, 3 MiB after. The MiB in flight across the
	// restart is unknowable and is not invented.
	if want := int64(7 << 20); total != want {
		t.Errorf("total = %d MiB, want %d MiB — a restart must not make it negative or tiny",
			total>>20, want>>20)
	}
}
