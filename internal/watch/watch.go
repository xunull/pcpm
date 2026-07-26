// Package watch holds the domain logic behind `pcpm watch`: the Process Trees
// the user has asked pcpm to keep measuring, and what it measures about them.
// See CONTEXT.md ("Watch Target", "Sample") and docs/adr/0007-0009.
package watch

import (
	"time"

	"github.com/xunull/pcpm/internal/proc"
)

// Target is a Watch Target: a Process Tree the user asked pcpm to keep
// measuring, named by the process it was added with.
//
// PID alone does not identify it. PIDs are recycled, so a target is pinned by
// the pair (PID, Created) — the same PID at a different creation time is a
// different process, and must not inherit the old one's history.
type Target struct {
	ID      int64 // assigned by the store; zero before it is stored
	PID     int32
	Created time.Time // when the process started
	Name    string
	Cmdline string
	Cwd     string // launch directory

	AddedAt time.Time
	// StoppedAt is when the user stopped watching, or nil while pcpm still is.
	// Stopping ends the watching, not the record: what the target was doing
	// before is still worth being able to ask about.
	StoppedAt *time.Time
}

// Watching reports whether pcpm is still collecting for this target.
func (t Target) Watching() bool { return t.StoppedAt == nil }

// Running reports whether the target's process is still on the machine. A PID
// that is present but started at a different time is a different process that
// happened to reuse the number, and does not count.
func (t Target) Running(ix proc.Index) bool {
	p, ok := ix.Lookup(t.PID)
	return ok && p.Created.Equal(t.Created)
}

// Status is a Target together with what is true of it right now, which the
// stored record alone cannot say: whether its process is still there.
type Status struct {
	Target
	Running bool
}

// Statuses pairs each target with the current state of its process.
func Statuses(targets []Target, ix proc.Index) []Status {
	out := make([]Status, len(targets))
	for i, t := range targets {
		out[i] = Status{Target: t, Running: t.Running(ix)}
	}
	return out
}
