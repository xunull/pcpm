package watch

import (
	"testing"
	"time"
)

func TestSaveAndReadSamples(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tgt, err := s.AddTarget(target(100, created), time.Now())
	if err != nil {
		t.Fatalf("AddTarget: %v", err)
	}

	at := time.Date(2026, 7, 1, 12, 0, 5, 0, time.UTC)
	// One tick: the root plus a child, each its own Sample. Samples are never
	// per tree — a tree figure has to stay decomposable afterwards.
	err = s.SaveSamples(tgt.ID, at, []Sample{
		{PID: 100, Created: created, Name: "bun", CPUSeconds: 12.5, RSSBytes: 300 << 20},
		{PID: 101, Created: created, Name: "node", CPUSeconds: 3.25, RSSBytes: 50 << 20},
	})
	if err != nil {
		t.Fatalf("SaveSamples: %v", err)
	}

	got, err := s.SamplesBetween(tgt.ID, at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil {
		t.Fatalf("SamplesBetween: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 samples, got %d", len(got))
	}
	if !got[0].At.Equal(at) {
		t.Errorf("At = %v, want %v", got[0].At, at)
	}
	// A counter read back as an integer would silently quantise every rate.
	if got[0].CPUSeconds != 12.5 {
		t.Errorf("CPUSeconds = %v, want 12.5 exactly", got[0].CPUSeconds)
	}
	if got[1].RSSBytes != 50<<20 {
		t.Errorf("RSSBytes = %d, want %d", got[1].RSSBytes, 50<<20)
	}
}

func TestSamplesBetweenIsBounded(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tgt, _ := s.AddTarget(target(100, created), time.Now())

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		at := base.Add(time.Duration(i) * 5 * time.Second)
		if err := s.SaveSamples(tgt.ID, at, []Sample{{PID: 100, Created: created, CPUSeconds: float64(i)}}); err != nil {
			t.Fatalf("SaveSamples: %v", err)
		}
	}

	// inclusive of from, exclusive of to — so adjacent windows neither drop a
	// sample nor count one twice
	got, err := s.SamplesBetween(tgt.ID, base.Add(5*time.Second), base.Add(15*time.Second))
	if err != nil {
		t.Fatalf("SamplesBetween: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 samples in [5s, 15s), got %d", len(got))
	}
	if got[0].CPUSeconds != 1 || got[1].CPUSeconds != 2 {
		t.Errorf("want the samples at 5s and 10s, got %v and %v", got[0].CPUSeconds, got[1].CPUSeconds)
	}
}

// Samples belong to a target, so one target's history must never leak into
// another's — including when the same PID is watched twice after being reused.
func TestSamplesAreScopedToTheirTarget(t *testing.T) {
	s := open(t)
	old := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	recycled := old.Add(48 * time.Hour)
	first, _ := s.AddTarget(target(100, old), time.Now())
	second, _ := s.AddTarget(target(100, recycled), time.Now())

	at := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := s.SaveSamples(first.ID, at, []Sample{{PID: 100, Created: old, CPUSeconds: 1}}); err != nil {
		t.Fatalf("SaveSamples: %v", err)
	}
	if err := s.SaveSamples(second.ID, at, []Sample{{PID: 100, Created: recycled, CPUSeconds: 99}}); err != nil {
		t.Fatalf("SaveSamples: %v", err)
	}

	got, _ := s.SamplesBetween(first.ID, at.Add(-time.Minute), at.Add(time.Minute))
	if len(got) != 1 || got[0].CPUSeconds != 1 {
		t.Errorf("the first target sees %v; the reused PID's samples leaked in", got)
	}
}

// The daemon re-reads its target list every tick, so this is the query that
// drives collection: stopped targets must drop out of it.
func TestWatchedTargetsExcludesStoppedOnes(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.AddTarget(target(100, created), time.Now()); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	if _, err := s.AddTarget(target(200, created), time.Now()); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	if _, err := s.StopTarget(200, time.Now()); err != nil {
		t.Fatalf("StopTarget: %v", err)
	}

	got, err := s.WatchedTargets()
	if err != nil {
		t.Fatalf("WatchedTargets: %v", err)
	}
	if len(got) != 1 || got[0].PID != 100 {
		t.Errorf("want only pid 100 still watched, got %+v", got)
	}
}

// Saving the same tick twice — a daemon restarted mid-tick, say — must not
// double the history.
func TestSaveSamplesIsIdempotentForATick(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tgt, _ := s.AddTarget(target(100, created), time.Now())
	at := time.Date(2026, 7, 1, 12, 0, 5, 0, time.UTC)
	sample := []Sample{{PID: 100, Created: created, CPUSeconds: 7}}

	if err := s.SaveSamples(tgt.ID, at, sample); err != nil {
		t.Fatalf("SaveSamples: %v", err)
	}
	if err := s.SaveSamples(tgt.ID, at, sample); err != nil {
		t.Fatalf("second SaveSamples: %v", err)
	}

	got, _ := s.SamplesBetween(tgt.ID, at.Add(-time.Minute), at.Add(time.Minute))
	if len(got) != 1 {
		t.Errorf("want 1 sample after saving the same tick twice, got %d", len(got))
	}
}
