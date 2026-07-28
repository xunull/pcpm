// Package top ranks the processes consuming CPU right now.
//
// The kernel keeps no such figure. What it keeps is a counter per process of
// CPU seconds consumed since that process started, which only ever grows. A
// rate exists only as a difference between two readings of that counter, so
// everything here is built around a pair of snapshots rather than a single
// measurement — the same reason ADR-0008 gave for storing counters in the watch
// tool rather than the percentages gopsutil offers.
package top

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/xunull/pcpm/internal/forgotten"
	"github.com/xunull/pcpm/internal/proc"
)

// Reading is one process's cumulative counters at one instant. Created is
// carried so that a PID reused between two snapshots can be told apart from the
// process that held it before.
type Reading struct {
	PID        int32
	Created    time.Time
	CPUSeconds float64 // since the process started, never a rate
	RSSBytes   int64
}

// Snapshot is every process that could be measured, at one instant.
type Snapshot struct {
	At       time.Time
	Readings []Reading
}

// Process is one row of a ranking: a process, and what it consumed over the
// window between the two snapshots it was derived from.
type Process struct {
	proc.Process
	// CPUPercent is per core: 100 means one core fully occupied, and a process
	// spread over eight cores reads 800. Dividing by the core count instead
	// would render the most common failure there is — one thread stuck in a
	// loop — as 10% on a ten-core machine.
	CPUPercent float64
	RSSBytes   int64
	// Forgotten marks a process belonging to a tree whose launching job is
	// gone. It is what a ranking can say that `top` cannot: not merely what is
	// busy, but which of it is busy for no reason anyone still remembers.
	Forgotten bool
}

// SortKey is what a ranking is ordered by.
type SortKey int

const (
	ByCPU SortKey = iota
	ByMemory
)

// ParseSortKey maps a --sort value to a SortKey. The empty string means the
// default.
func ParseSortKey(s string) (SortKey, error) {
	switch s {
	case "", "cpu":
		return ByCPU, nil
	case "mem", "memory":
		return ByMemory, nil
	default:
		return ByCPU, fmt.Errorf("invalid sort key %q: want \"cpu\" or \"mem\"", s)
	}
}

// Owner selects whose processes a ranking covers. Its zero value covers
// everyone, so a caller that forgets to set it gets too much rather than the
// wrong thing.
type Owner struct {
	uid  int32
	only bool
}

// AnyOwner ranks every process, whoever owns it. This is what running as root
// makes possible, and what the zero value already means.
func AnyOwner() Owner { return Owner{} }

// OwnedBy ranks only the processes belonging to uid.
//
// Without privilege there is no alternative on macOS: another user's process
// reports zero CPU and zero memory *without an error*, because `ps` and `top`
// are setuid root and pcpm is not. Ranking those alongside real figures would
// sort the machine's busiest processes to the bottom of a list whose whole
// purpose is the ordering (ADR-0011).
func OwnedBy(uid int32) Owner { return Owner{uid: uid, only: true} }

func (o Owner) covers(p proc.Process) bool { return !o.only || p.UID == o.uid }

// Options shapes a ranking.
type Options struct {
	Sort  SortKey
	Owner Owner
	Limit int // rows to keep; 0 keeps all
}

// Rank derives per-process rates from two snapshots and orders them, busiest
// first. known supplies the static facts for a PID; a PID it does not describe
// is left out, as is one that appears in only one of the two snapshots.
func Rank(before, after Snapshot, known map[int32]proc.Process, opt Options) []Process {
	elapsed := after.At.Sub(before.At).Seconds()
	if elapsed <= 0 {
		return nil
	}

	prior := make(map[int32]Reading, len(before.Readings))
	for _, r := range before.Readings {
		prior[r.PID] = r
	}

	out := make([]Process, 0, len(after.Readings))
	for _, now := range after.Readings {
		facts, ok := known[now.PID]
		if !ok || !opt.Owner.covers(facts) {
			continue
		}
		was, ok := prior[now.PID]
		if !ok {
			// No earlier counter to subtract from. Using the lifetime total
			// instead would put a long-lived process pcpm has only just noticed
			// straight to the top of the ranking.
			continue
		}
		if !was.Created.Equal(now.Created) {
			continue // the PID was reused; these are two different processes
		}
		delta := now.CPUSeconds - was.CPUSeconds
		if delta < 0 {
			continue // a counter that went backwards is not a measurement
		}
		out = append(out, Process{
			Process:    facts,
			CPUPercent: delta / elapsed * 100,
			RSSBytes:   now.RSSBytes,
		})
	}

	slices.SortFunc(out, opt.Sort.compare)
	if opt.Limit > 0 && len(out) > opt.Limit {
		out = out[:opt.Limit]
	}
	return out
}

// compare orders two rows. The secondary keys matter more than they look: equal
// rates are common — most processes are idle — and without a total order the
// rows below the busy ones would shuffle on every frame of a live view.
func (k SortKey) compare(a, b Process) int {
	first, second := a.CPUPercent, b.CPUPercent
	third, fourth := a.RSSBytes, b.RSSBytes
	if k == ByMemory {
		first, second = float64(a.RSSBytes), float64(b.RSSBytes)
		third, fourth = int64(a.CPUPercent*1000), int64(b.CPUPercent*1000)
	}
	switch {
	case first != second:
		return descending(first, second)
	case third != fourth:
		return descending(float64(third), float64(fourth))
	default:
		return int(a.PID - b.PID)
	}
}

func descending(a, b float64) int {
	if a > b {
		return -1
	}
	return 1
}

// Machine is the host as a ranking needs it: a cheap way to read every
// process's counters, and a dearer way to learn what a process is.
//
// The split is the whole reason a ranking can refresh once a second. Measured
// on a machine running 1100 processes: the counters cost about 30ms all told,
// while the names, command lines and launch directories cost another 50ms — and
// those never change, so they are read once per process rather than per frame.
type Machine interface {
	Readings() ([]Reading, error)
	Describe(pid int32) (proc.Process, error)
}

// Host measures the machine pcpm is running on.
type Host struct{}

// Readings reads every process's cumulative counters.
//
// A process that cannot be read at all is skipped; one whose memory cannot be
// read keeps its CPU counter, since a missing figure is worth less than a wrong
// ranking.
func (Host) Readings() ([]Reading, error) {
	ps, err := process.Processes()
	if err != nil {
		return nil, err
	}
	out := make([]Reading, 0, len(ps))
	for _, p := range ps {
		times, err := p.Times()
		if err != nil {
			continue
		}
		r := Reading{PID: p.Pid, CPUSeconds: times.User + times.System}
		if ms, err := p.CreateTime(); err == nil && ms > 0 {
			r.Created = time.UnixMilli(ms)
		}
		if mem, err := p.MemoryInfo(); err == nil && mem != nil {
			r.RSSBytes = int64(mem.RSS)
		}
		out = append(out, r)
	}
	return out, nil
}

func (Host) Describe(pid int32) (proc.Process, error) { return proc.Describe(pid) }

// Sampler takes snapshots, remembering what does not change between them.
type Sampler struct {
	machine Machine
	facts   map[int32]proc.Process
}

func NewSampler(m Machine) *Sampler {
	return &Sampler{machine: m, facts: map[int32]proc.Process{}}
}

// Take reads every process's counters, describing any it has not seen before.
//
// The facts it holds are rebuilt each time rather than added to, so a process
// that has exited stops being remembered — a view left open for hours would
// otherwise accumulate every process the machine ever ran.
func (s *Sampler) Take(now time.Time) (Snapshot, error) {
	readings, err := s.machine.Readings()
	if err != nil {
		return Snapshot{}, err
	}
	fresh := make(map[int32]proc.Process, len(readings))
	kept := make([]Reading, 0, len(readings))
	for _, r := range readings {
		facts, known := s.facts[r.PID]
		if !known || !facts.Created.Equal(r.Created) {
			described, err := s.machine.Describe(r.PID)
			if err != nil {
				// Exited between being listed and being described. Ordinary on
				// a busy machine, and not a reason to fail the snapshot.
				continue
			}
			facts = described
		}
		fresh[r.PID] = facts
		kept = append(kept, r)
	}
	s.facts = fresh
	return Snapshot{At: now, Readings: kept}, nil
}

// Facts returns what the sampler knows about the processes in its last
// snapshot.
func (s *Sampler) Facts() map[int32]proc.Process { return s.facts }

// Ranker produces one ranking after another from a running Machine, keeping the
// previous snapshot so that every frame after the first costs a single read.
type Ranker struct {
	sampler  *Sampler
	previous Snapshot
	started  bool
	Options  Options
}

func NewRanker(m Machine, opt Options) *Ranker {
	return &Ranker{sampler: NewSampler(m), Options: opt}
}

// Next takes a snapshot and ranks it against the one before it. The first call
// has nothing to compare against and reports no rows and no error — a rate
// needs two readings, and there is no honest answer until the second.
func (r *Ranker) Next(now time.Time) ([]Process, error) {
	snap, err := r.sampler.Take(now)
	if err != nil {
		return nil, err
	}
	previous, started := r.previous, r.started
	r.previous, r.started = snap, true
	if !started {
		return nil, nil
	}
	known := r.sampler.Facts()
	rows := Rank(previous, snap, known, r.Options)
	unattended := Forgotten(known)
	for i := range rows {
		rows[i].Forgotten = unattended[rows[i].PID]
	}
	return rows, nil
}

// bundleSuffix marks a macOS application bundle directory.
const bundleSuffix = ".app"

// Application returns the macOS application an executable belongs to, or "" for
// one that belongs to none.
//
// It is the *first* .app component of the path, not the last. Bundles nest —
// `Chrome.app/…/Chrome Helper (Renderer).app/…` — and taking the last one would
// group nothing, because every helper is its own bundle.
//
// A process outside any bundle has no application, rather than inheriting one
// from whatever launched it. Walking up the parent chain instead would label
// `claude` as `Warp` and every command as its terminal, which points at the
// wrong thing with more confidence than saying nothing.
//
// On Linux nothing matches, and the column that shows this disappears of its
// own accord.
func Application(exe string) string {
	segments := strings.Split(exe, "/")
	// The last segment is the executable itself. A bundle is a directory that
	// contains one, so a path ending at a .app groups nothing.
	for _, s := range segments[:max(len(segments)-1, 0)] {
		if name, ok := strings.CutSuffix(s, bundleSuffix); ok && name != "" {
			return name
		}
	}
	return ""
}

// Application returns the macOS application this process belongs to, or "".
func (p Process) Application() string { return Application(p.Exe) }

// Forgotten returns the PIDs belonging to a Forgotten Process Tree — a tree
// whose launching job is gone, as `pcpm forgotten` defines it.
//
// Every member is included, not only the root. The process actually burning the
// CPU is frequently a child, and marking roots alone would leave the row a
// reader is looking at unmarked while flagging one further down the list.
func Forgotten(known map[int32]proc.Process) map[int32]bool {
	all := make([]proc.Process, 0, len(known))
	for _, p := range known {
		all = append(all, p)
	}
	ix := proc.NewIndex(all)

	marked := map[int32]bool{}
	for _, tree := range forgotten.Detect(all, nil) {
		for _, pid := range ix.TreeMembers(tree.Root.PID) {
			marked[pid] = true
		}
	}
	return marked
}
