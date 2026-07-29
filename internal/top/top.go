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
	"math"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"

	"github.com/xunull/pcpm/internal/forgotten"
	"github.com/xunull/pcpm/internal/proc"
)

// Defaults for the ranking. They live here rather than in the config package so
// that configuration and code cannot drift apart on what "the default" is.
//
// Two seconds, rather than the one second top(1) uses, because the Interval is
// both the refresh period and the window each figure averages over: a redraw
// slow enough to read is the same setting as a figure steady enough to trust.
// It cannot be shortened for responsiveness without also making the figures
// noisier, nor lengthened for calm without flattening brief spikes into it.
const (
	DefaultInterval = 2 * time.Second
	// MinInterval is the shortest Interval worth honouring.
	//
	// Below it the tool becomes a significant part of what it measures. One
	// sample of this machine's 1152 processes costs 30–48ms once their names
	// are cached, so at 100ms pcpm would spend a quarter to a half of every
	// Interval measuring — enough CPU to put itself in its own ranking. The
	// first sample, which has to describe every process, costs 274ms, so no
	// shorter Interval can be met on the first frame anyway.
	//
	// It is not about correctness: a rate is derived from the real elapsed time
	// between two snapshots rather than from the nominal Interval, so the
	// arithmetic holds however long the sampling took.
	MinInterval = 200 * time.Millisecond
	// DefaultRows is how many rows to print where there is no window to fill.
	DefaultRows = 10
	// FitWindow, as a row count, means "as many as the terminal holds" — and
	// DefaultRows where there is no terminal. It is the built-in default so
	// that a number given in a config file behaves exactly like one given on
	// the command line: both are an explicit choice, and only the absence of
	// one lets the window decide.
	FitWindow = 0
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

// Snapshot is every process that could be measured, at one instant, together
// with what the machine as a whole was doing.
type Snapshot struct {
	At       time.Time
	Readings []Reading
	System   SystemReading
}

// SystemReading is the machine's own counters at one instant. None of it needs
// privilege, which is what lets the gap left by the per-process figures be
// stated as a quantity rather than left as an absence.
type SystemReading struct {
	BusySeconds      float64 // cumulative across all cores, idle excluded
	Cores            int
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
}

// Sum is what a set of rows comes to. A narrowed ranking uses it to say how
// much of the whole it still accounts for, and the whole ranking's own Sum is
// what the header's attributed figure is made of.
type Sum struct {
	Count      int
	CPUPercent float64
	RSSBytes   int64
}

// Total adds up rows.
//
// Callers take it over every row they mean rather than the ones that fit on a
// screen, so that the figure describes the rows and not the height of the
// terminal it happens to be read in.
func Total(rows []Process) Sum {
	s := Sum{Count: len(rows)}
	for _, p := range rows {
		s.CPUPercent += p.CPUPercent
		s.RSSBytes += p.RSSBytes
	}
	return s
}

// Totals is what the machine did over a window, and how much of it the ranking
// could account for.
type Totals struct {
	Cores int
	// BusyPercent is per core on the same scale as a row: 699 means just under
	// seven of ten cores were occupied.
	BusyPercent float64
	// ranked is what the frame's rows come to. The header quotes the CPU share
	// of it; anything else that has to agree with the header reads it from here
	// rather than adding the rows up again, so the two cannot drift apart.
	ranked           Sum
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	// Complete says the ranking covered every process on the machine, which is
	// only true when running as root. When false, the unattributed figure has a
	// remedy worth naming.
	Complete bool
}

// UnattributedPercent is the busy CPU the ranking could not assign to any
// process — kernel_task, which gopsutil refuses outright as PID 0, and the
// system daemons that report zero to an unprivileged reader.
//
// It does not fall to zero when Complete is true. kernel_task cannot be read at
// any privilege, so a root ranking still leaves a residual, and that residual is
// worth seeing rather than assuming away.
//
// It is clamped at zero: the host counters and the per-process counters are
// read microseconds apart, and a negative would be that skew, not a fact.
func (t Totals) UnattributedPercent() float64 {
	return math.Max(t.BusyPercent-t.AttributedPercent(), 0)
}

// Ranked is what the frame's rows came to: how many there were, and what they
// consumed between them.
func (t Totals) Ranked() Sum { return t.ranked }

// AttributedPercent is the busy CPU the ranking assigned to processes, which is
// by construction the sum over its rows.
func (t Totals) AttributedPercent() float64 { return t.ranked.CPUPercent }

// WithRanked returns the totals with the ranking's own figures set. It exists
// for tests that need a Totals without a ranking behind it.
func (t Totals) WithRanked(s Sum) Totals { t.ranked = s; return t }

// Capacity is what every core in the machine running flat out would read.
func (t Totals) Capacity() float64 { return float64(t.Cores) * 100 }

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

// Covers reports whether a ranking with this owner includes the process.
func (o Owner) Covers(p proc.Process) bool { return !o.only || p.UID == o.uid }

// complete reports whether this owner leaves nothing out.
func (o Owner) complete() bool { return !o.only }

// Options shapes a ranking.
//
// There is deliberately no row limit here. A ranking always covers every
// process it can measure, because the header has to account for all of them;
// how many fit on a screen is a question about the terminal, answered by Top.
type Options struct {
	Sort  SortKey
	Owner Owner
}

// Top returns at most n rows from an ordered ranking, or all of them when n is
// not positive.
func Top(rows []Process, n int) []Process {
	if n > 0 && len(rows) > n {
		return rows[:n]
	}
	return rows
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
		if !ok || !opt.Owner.Covers(facts) {
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

	Sort(out, opt.Sort)
	return out
}

// Sort orders a ranking in place, busiest first by the given key.
func Sort(rows []Process, by SortKey) { slices.SortFunc(rows, by.compare) }

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
// process's counters, a dearer way to learn what a process is, and the
// machine's own totals.
//
// The split is the whole reason a ranking can refresh at all. Measured on a
// machine running 1152 processes: a steady frame costs 30–48ms, because names,
// command lines and launch directories never change and so are read once per
// process rather than once per frame. The first frame, which has to describe
// every process, costs 274ms — nine times a steady one, and the reason
// MinInterval is what it is.
type Machine interface {
	Readings() ([]Reading, error)
	Describe(pid int32) (proc.Process, error)
	System() (SystemReading, error)
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

// System reads the machine's own counters. Unlike the per-process figures these
// need no privilege, so they are true even when most of the processes behind
// them are unreadable.
func (Host) System() (SystemReading, error) {
	h := SystemReading{Cores: runtime.NumCPU()}
	times, err := cpu.Times(false)
	if err != nil {
		return SystemReading{}, err
	}
	if len(times) > 0 {
		h.BusySeconds = times[0].Total() - times[0].Idle
	}
	// Memory is worth less than CPU here and should not fail the reading.
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		h.MemoryUsedBytes = int64(vm.Used)
		h.MemoryTotalBytes = int64(vm.Total)
	}
	return h, nil
}

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
	system, err := s.machine.System()
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
	return Snapshot{At: now, Readings: kept, System: system}, nil
}

// Facts returns what the sampler knows about the processes in its last
// snapshot.
func (s *Sampler) Facts() map[int32]proc.Process { return s.facts }

// Frame is one ranking together with the machine's own figures for the same
// window, so that what the rows account for can be compared against what the
// machine actually did.
type Frame struct {
	At     time.Time
	Rows   []Process
	Totals Totals
}

// Ranker produces one frame after another from a running Machine, keeping the
// previous snapshot so that every frame after the first costs a single read.
type Ranker struct {
	sampler  *Sampler
	previous Snapshot
	started  bool
	Options  Options

	// Ignore silences the forgotten marker for the same trees `pcpm forgotten`
	// would leave out, so one setting governs both.
	Ignore []string
}

func NewRanker(m Machine, opt Options) *Ranker {
	return &Ranker{sampler: NewSampler(m), Options: opt}
}

// Next takes a snapshot and ranks it against the one before it. The first call
// has nothing to compare against and returns nil with no error — a rate needs
// two readings, and there is no honest answer until the second.
func (r *Ranker) Next(now time.Time) (*Frame, error) {
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

	// The attributed figure has to cover every process pcpm could see, not
	// merely the ones that will fit on screen, or the header's arithmetic
	// would change with the terminal's height.
	rows := Rank(previous, snap, known, r.Options)

	forgottenPIDs := Forgotten(known, r.Ignore)
	for i := range rows {
		rows[i].Forgotten = forgottenPIDs[rows[i].PID]
	}

	elapsed := snap.At.Sub(previous.At).Seconds()
	totals := Totals{
		Cores: snap.System.Cores,
		// Summed once, here. The header quotes the CPU share of this and a
		// narrowed view quotes all three parts of it, so adding the rows up a
		// second time somewhere else is how the two would come to disagree.
		ranked:           Total(rows),
		MemoryUsedBytes:  snap.System.MemoryUsedBytes,
		MemoryTotalBytes: snap.System.MemoryTotalBytes,
		Complete:         r.Options.Owner.complete(),
	}
	if elapsed > 0 {
		totals.BusyPercent = math.Max(snap.System.BusySeconds-previous.System.BusySeconds, 0) / elapsed * 100
	}

	return &Frame{At: snap.At, Rows: rows, Totals: totals}, nil
}

// bundleSuffix marks a macOS application bundle directory.
//
// cloneSuffix is what macOS appends when it runs an application from a
// code-signing clone: the bundle appears as "Google Chrome.app.bundle" under
// /private/var/folders. Chrome's main process does this while its helpers run
// from /Applications, so missing it left the busiest row of a real ranking with
// no application while the rows beneath it had one.
const (
	bundleSuffix = ".app"
	cloneSuffix  = ".bundle"
)

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
		if name, ok := strings.CutSuffix(strings.TrimSuffix(s, cloneSuffix), bundleSuffix); ok && name != "" {
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
//
// ignore silences the same trees `pcpm forgotten` would silence. Without it a
// process the reader has deliberately excused still carries the mark, and the
// legend sends them to a command that lists nothing.
func Forgotten(known map[int32]proc.Process, ignore []string) map[int32]bool {
	all := make([]proc.Process, 0, len(known))
	for _, p := range known {
		all = append(all, p)
	}
	ix := proc.NewIndex(all)

	trees := forgotten.Detect(all, nil)
	// A malformed pattern is the config's problem, reported where the config is
	// read; here it simply silences nothing.
	if kept, err := forgotten.ApplyIgnore(trees, ignore); err == nil {
		trees = kept
	}

	marked := map[int32]bool{}
	for _, tree := range trees {
		for _, pid := range ix.TreeMembers(tree.Root.PID) {
			marked[pid] = true
		}
	}
	return marked
}
