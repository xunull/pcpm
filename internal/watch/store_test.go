package watch

import (
	"path/filepath"
	"testing"
	"time"
)

// open returns a Store backed by a real database in a temp dir. These tests run
// against SQLite rather than a fake: the schema and its constraints are most of
// what is being tested.
func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "pcpm.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func target(pid int32, created time.Time) Target {
	return Target{
		PID:     pid,
		Created: created,
		Name:    "bun",
		Cmdline: "bun run dev",
		Cwd:     "/proj",
	}
}

func TestAddAndListTargets(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	added, err := s.AddTarget(target(100, created), time.Now())
	if err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	if added.ID == 0 {
		t.Error("a stored target should carry an ID")
	}

	got, err := s.Targets()
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 target, got %d", len(got))
	}
	if got[0].PID != 100 || got[0].Cwd != "/proj" {
		t.Errorf("stored target = %+v, want pid 100 in /proj", got[0])
	}
	// The creation time is what makes a target identifiable after PID reuse, so
	// it has to survive the round trip.
	if !got[0].Created.Equal(created) {
		t.Errorf("Created = %v, want %v", got[0].Created, created)
	}
}

func TestAddTargetIsIdempotent(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	first, err := s.AddTarget(target(100, created), time.Now())
	if err != nil {
		t.Fatalf("first AddTarget: %v", err)
	}
	again, err := s.AddTarget(target(100, created), time.Now())
	if err != nil {
		t.Fatalf("second AddTarget: %v", err)
	}

	if again.ID != first.ID {
		t.Errorf("re-adding the same process made a second target (%d then %d)", first.ID, again.ID)
	}
	if got, _ := s.Targets(); len(got) != 1 {
		t.Errorf("want 1 target after adding the same process twice, got %d", len(got))
	}
}

// The same PID at a different creation time is a different process — the PID
// was reused. It must become its own target, not silently join the old one.
func TestSamePIDReusedIsADistinctTarget(t *testing.T) {
	s := open(t)
	old := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	recycled := old.Add(48 * time.Hour)

	if _, err := s.AddTarget(target(100, old), time.Now()); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	if _, err := s.AddTarget(target(100, recycled), time.Now()); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}

	got, _ := s.Targets()
	if len(got) != 2 {
		t.Fatalf("want 2 distinct targets for a reused PID, got %d", len(got))
	}
}

// Removing stops the watching; it must not throw away what was collected, or
// "what was it doing before it died" stops being answerable.
func TestStopTargetKeepsTheRow(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.AddTarget(target(100, created), time.Now()); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}

	stoppedAt := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)
	n, err := s.StopTarget(100, stoppedAt)
	if err != nil {
		t.Fatalf("StopTarget: %v", err)
	}
	if n != 1 {
		t.Errorf("StopTarget reported %d targets stopped, want 1", n)
	}

	got, _ := s.Targets()
	if len(got) != 1 {
		t.Fatalf("stopping deleted the target; want it kept, got %d rows", len(got))
	}
	if got[0].StoppedAt == nil {
		t.Fatal("StoppedAt is nil; want the time it was stopped")
	}
	if !got[0].StoppedAt.Equal(stoppedAt) {
		t.Errorf("StoppedAt = %v, want %v", *got[0].StoppedAt, stoppedAt)
	}
}

func TestStopTargetThatIsNotWatched(t *testing.T) {
	s := open(t)

	n, err := s.StopTarget(999, time.Now())
	if err != nil {
		t.Fatalf("stopping an unwatched pid should not error: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0 targets stopped, got %d", n)
	}
}

// Watching is resumable: adding a stopped target again picks the same row back
// up rather than leaving a second, duplicate history.
func TestAddingAStoppedTargetResumesIt(t *testing.T) {
	s := open(t)
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	first, _ := s.AddTarget(target(100, created), time.Now())
	if _, err := s.StopTarget(100, time.Now()); err != nil {
		t.Fatalf("StopTarget: %v", err)
	}

	again, err := s.AddTarget(target(100, created), time.Now())
	if err != nil {
		t.Fatalf("re-adding: %v", err)
	}

	if again.ID != first.ID {
		t.Errorf("resuming made a new target (%d then %d)", first.ID, again.ID)
	}
	got, _ := s.Targets()
	if len(got) != 1 || got[0].StoppedAt != nil {
		t.Errorf("want a single watched target after resuming, got %+v", got)
	}
}

// Opening an existing database must not wipe or re-create it.
func TestOpenIsIdempotentAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pcpm.db")
	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := first.AddTarget(target(100, created), time.Now()); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer second.Close()

	got, err := second.Targets()
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("re-opening lost the data: want 1 target, got %d", len(got))
	}
}
