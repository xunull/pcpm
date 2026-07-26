// Package proc is pcpm's view of the process table: the facts every tool needs
// about a process, and the parent/child structure they navigate. It has no
// opinion about which processes are interesting — that belongs to the tools
// built on top of it.
package proc

import "time"

// Process is a single process observed on the host, reduced to the facts pcpm's
// tools reason about: who launched it, what group it belongs to, where it was
// started from, and how long it has been running.
type Process struct {
	PID     int32
	PPID    int32
	PGID    int32 // process group; a setsid'd daemon has PGID == PID
	UID     int32
	User    string
	Name    string
	Cmdline string
	Cwd     string // launch directory
	Created time.Time
}

// Index is one snapshot of the process table, keyed for lookup and for walking
// Process Trees. It is a view over the processes it was built from, not a live
// handle on the machine: anything that starts or exits afterwards is not in it.
type Index struct {
	byPID    map[int32]Process
	children map[int32][]int32
}

// NewIndex keys a set of processes for lookup and tree walking.
func NewIndex(procs []Process) Index {
	ix := Index{
		byPID:    make(map[int32]Process, len(procs)),
		children: make(map[int32][]int32, len(procs)),
	}
	for _, p := range procs {
		ix.byPID[p.PID] = p
	}
	for _, p := range procs {
		ix.children[p.PPID] = append(ix.children[p.PPID], p.PID)
	}
	return ix
}

// Lookup returns the process with this PID and whether the snapshot held it.
// Absence is meaningful: it is how a caller learns that a process is gone — for
// instance that a process group's leader is dead.
func (ix Index) Lookup(pid int32) (Process, bool) {
	p, ok := ix.byPID[pid]
	return p, ok
}

// Ancestors returns pid's ancestors, nearest first, stopping where the chain
// leaves the snapshot. pid itself is not among them, and a pid the snapshot
// never held has no known ancestors.
//
// Cycles terminate the walk for the same reason they do in TreeMembers.
func (ix Index) Ancestors(pid int32) []Process {
	current, ok := ix.byPID[pid]
	if !ok {
		return nil
	}
	var out []Process
	seen := map[int32]bool{pid: true}
	for {
		parent, ok := ix.byPID[current.PPID]
		if !ok || seen[parent.PID] {
			return out
		}
		seen[parent.PID] = true
		out = append(out, parent)
		current = parent
	}
}

// TreeMembers returns root together with every descendant of it, breadth-first.
// A root that is not in the snapshot yields just itself.
//
// Parent links are read one process at a time, so a snapshot can be internally
// inconsistent and describe a cycle; each PID is visited once, which makes the
// walk terminate on one rather than hang.
func (ix Index) TreeMembers(root int32) []int32 {
	members := []int32{root}
	seen := map[int32]bool{root: true}
	for i := 0; i < len(members); i++ {
		for _, kid := range ix.children[members[i]] {
			if !seen[kid] {
				seen[kid] = true
				members = append(members, kid)
			}
		}
	}
	return members
}
