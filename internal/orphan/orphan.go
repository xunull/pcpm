// Package orphan holds the pure domain logic for finding orphaned application
// processes — processes reparented to init (PPID 1) that belong to a real
// login user rather than root or a system service account. See CONTEXT.md for
// the glossary and docs/adr for the decisions behind these rules.
package orphan

import (
	"sort"
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
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Created.Before(out[j].Created)
	})
	return out
}
