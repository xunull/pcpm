package orphan

import (
	"testing"
	"time"
)

func TestCandidates(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	procs := []Process{
		{PID: 10, PPID: 1, UID: 501, Name: "next-server", Created: base.Add(2 * time.Hour)}, // candidate, newer
		{PID: 11, PPID: 1, UID: 0, Name: "root-daemon"},                                     // root -> excluded
		{PID: 12, PPID: 42, UID: 501, Name: "child-of-shell"},                               // PPID != 1 -> excluded
		{PID: 13, PPID: 1, UID: 205, Name: "_locationd"},                                    // service acct (uid < minUID) -> excluded
		{PID: 14, PPID: 1, UID: 501, Name: "vite", Created: base},                           // candidate, older
	}

	got := Candidates(procs, 500)

	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %d: %+v", len(got), got)
	}
	// oldest-first: PID 14 (base) before PID 10 (base+2h)
	if got[0].PID != 14 || got[1].PID != 10 {
		t.Errorf("want oldest-first [14, 10], got [%d, %d]", got[0].PID, got[1].PID)
	}
}

func TestDefaultMinUID(t *testing.T) {
	if got := DefaultMinUID("darwin"); got != 500 {
		t.Errorf("darwin: want 500, got %d", got)
	}
	if got := DefaultMinUID("linux"); got != 1000 {
		t.Errorf("linux: want 1000, got %d", got)
	}
}

func TestApplyIgnore(t *testing.T) {
	procs := []Process{
		{PID: 1, Name: "next-server"},
		{PID: 2, Name: "ssh-agent"},
		{PID: 3, Name: "com.apple.helper"},
		{PID: 4, Name: "vite"},
	}

	// exact match ("ssh-agent") and glob ("*.helper") are dropped; order kept
	got, err := ApplyIgnore(procs, []string{"ssh-agent", "*.helper"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].PID != 1 || got[1].PID != 4 {
		t.Fatalf("want kept [1, 4], got %+v", got)
	}

	// no patterns keeps everything
	all, err := ApplyIgnore(procs, nil)
	if err != nil || len(all) != 4 {
		t.Errorf("nil patterns: want 4 kept and no error, got %d err=%v", len(all), err)
	}

	// a malformed glob is reported, not silently ignored
	if _, err := ApplyIgnore(procs, []string{"["}); err == nil {
		t.Error("want error for malformed pattern \"[\", got nil")
	}
}
