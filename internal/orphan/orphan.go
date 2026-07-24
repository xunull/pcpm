// Package orphan holds the pure domain logic for finding orphaned application
// processes — processes reparented to init (PPID 1) that belong to a real
// login user rather than root or a system service account. See CONTEXT.md for
// the glossary and docs/adr for the decisions behind these rules.
package orphan

import (
	"fmt"
	"path"
	"slices"
	"time"
)

// Process is a single process observed on the host, reduced to the fields pcpm
// needs to decide whether it is an orphaned application process candidate.
type Process struct {
	PID     int32
	PPID    int32
	UID     int32
	User    string
	Name    string
	Cmdline string
	Created time.Time // process start time
}

// DefaultMinUID returns the smallest UID treated as a real login user on the
// given OS: 500 on macOS (human users start at 501) and 1000 elsewhere (the
// Linux UID_MIN convention). Below this are system and service accounts.
func DefaultMinUID(goos string) int32 {
	switch goos {
	case "darwin":
		return 500
	default:
		return 1000
	}
}

// Candidates returns the processes that look like orphaned application process
// candidates: reparented to init (PPID == 1) and owned by a real login user
// (UID != 0 and UID >= minUID). The result is sorted oldest-first (earliest
// Created first), so the longest-lived — most suspicious — orphans come first.
func Candidates(procs []Process, minUID int32) []Process {
	var out []Process
	for _, p := range procs {
		if p.PPID == 1 && p.UID != 0 && p.UID >= minUID {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b Process) int {
		return a.Created.Compare(b.Created)
	})
	return out
}

// ApplyIgnore returns the processes whose Name matches none of the glob
// patterns. Patterns use path.Match syntax (e.g. "ssh-agent", "*.helper"); this
// is how a user suppresses known-good or intentionally-backgrounded processes.
// A malformed pattern is reported as an error rather than silently ignored.
func ApplyIgnore(procs []Process, patterns []string) ([]Process, error) {
	for _, pat := range patterns {
		if _, err := path.Match(pat, ""); err != nil {
			return nil, fmt.Errorf("invalid ignore pattern %q: %w", pat, err)
		}
	}
	if len(patterns) == 0 {
		return procs, nil
	}
	out := make([]Process, 0, len(procs))
	for _, p := range procs {
		if !ignored(p.Name, patterns) {
			out = append(out, p)
		}
	}
	return out, nil
}

// ignored reports whether name matches any of the glob patterns. Patterns are
// assumed valid (ApplyIgnore checks them up front).
func ignored(name string, patterns []string) bool {
	for _, pat := range patterns {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}
