package top

import (
	"errors"
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/proc"
)

var epoch = time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)

// facts builds a lookup for processes that only differ in the ways a test cares
// about.
func facts(procs ...proc.Process) map[int32]proc.Process {
	m := make(map[int32]proc.Process, len(procs))
	for _, p := range procs {
		m[p.PID] = p
	}
	return m
}

func at(offset time.Duration, readings ...Reading) Snapshot {
	return Snapshot{At: epoch.Add(offset), Readings: readings}
}

// The whole point of taking two snapshots: a rate is a difference. A process
// that has burned six CPU-seconds since boot but none during the window is
// idle, whatever its lifetime average says.
func TestRateComesFromTheDifferenceNotTheTotal(t *testing.T) {
	before := at(0,
		Reading{PID: 1, CPUSeconds: 6000},
		Reading{PID: 2, CPUSeconds: 0},
	)
	after := at(time.Second,
		Reading{PID: 1, CPUSeconds: 6000},
		Reading{PID: 2, CPUSeconds: 1},
	)

	got := Rank(before, after, facts(
		proc.Process{PID: 1, Name: "old-and-idle"},
		proc.Process{PID: 2, Name: "young-and-busy"},
	), Options{})

	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0].Name != "young-and-busy" {
		t.Errorf("busiest is %q, want young-and-busy — a lifetime average would say otherwise", got[0].Name)
	}
	if got[0].CPUPercent != 100 {
		t.Errorf("one CPU-second in one second = %v%%, want 100", got[0].CPUPercent)
	}
	if got[1].CPUPercent != 0 {
		t.Errorf("idle process reads %v%%, want 0", got[1].CPUPercent)
	}
}

// Per core, not per machine: eight CPU-seconds in one second is eight cores,
// and saying "80% of a ten-core box" would hide that this is a big number.
func TestPercentagesArePerCoreAndMayExceedOneHundred(t *testing.T) {
	before := at(0, Reading{PID: 1, CPUSeconds: 0})
	after := at(time.Second, Reading{PID: 1, CPUSeconds: 8})

	got := Rank(before, after, facts(proc.Process{PID: 1}), Options{})

	if got[0].CPUPercent != 800 {
		t.Errorf("CPUPercent = %v, want 800", got[0].CPUPercent)
	}
}

func TestRateDividesByTheActualElapsedTime(t *testing.T) {
	before := at(0, Reading{PID: 1, CPUSeconds: 0})
	after := at(2*time.Second, Reading{PID: 1, CPUSeconds: 1})

	got := Rank(before, after, facts(proc.Process{PID: 1}), Options{})

	if got[0].CPUPercent != 50 {
		t.Errorf("one CPU-second over two seconds = %v%%, want 50", got[0].CPUPercent)
	}
}

// A process that appeared during the window has no earlier counter to subtract
// from, so there is no rate to report yet. Treating its lifetime total as the
// window's consumption would put a long-lived process that pcpm has only just
// noticed straight to the top.
func TestAProcessSeenOnlyInTheSecondSnapshotIsNotRanked(t *testing.T) {
	before := at(0, Reading{PID: 1, CPUSeconds: 0})
	after := at(time.Second,
		Reading{PID: 1, CPUSeconds: 0},
		Reading{PID: 2, CPUSeconds: 500},
	)

	got := Rank(before, after, facts(
		proc.Process{PID: 1, Name: "known"},
		proc.Process{PID: 2, Name: "newcomer"},
	), Options{})

	for _, p := range got {
		if p.Name == "newcomer" {
			t.Errorf("a process with no earlier reading was ranked at %v%%", p.CPUPercent)
		}
	}
}

// A PID is a slot, not an identity. If it is reused within the window, the new
// process's counter starts near zero and subtracting the old one yields a
// negative — which must not become a rate, and must not be attributed to
// whichever of the two processes the facts happen to describe.
func TestAReusedPIDIsNotRanked(t *testing.T) {
	before := at(0, Reading{PID: 1, Created: epoch, CPUSeconds: 500})
	after := at(time.Second, Reading{PID: 1, Created: epoch.Add(500 * time.Millisecond), CPUSeconds: 0})

	got := Rank(before, after, facts(proc.Process{PID: 1}), Options{})

	if len(got) != 0 {
		t.Errorf("a reused PID was ranked: %+v", got)
	}
}

// On macOS another user's process reports zero CPU and zero memory without an
// error, so ranking them would sort the machine's busiest processes last.
func TestOnlyTheOwnersProcessesAreRanked(t *testing.T) {
	before := at(0,
		Reading{PID: 1, CPUSeconds: 0},
		Reading{PID: 2, CPUSeconds: 0},
	)
	after := at(time.Second,
		Reading{PID: 1, CPUSeconds: 1},
		Reading{PID: 2, CPUSeconds: 0},
	)
	f := facts(
		proc.Process{PID: 1, UID: 501, Name: "mine"},
		proc.Process{PID: 2, UID: 0, Name: "roots"},
	)

	got := Rank(before, after, f, Options{Owner: OwnedBy(501)})
	if len(got) != 1 || got[0].Name != "mine" {
		t.Errorf("want only the owner's process, got %+v", got)
	}

	all := Rank(before, after, f, Options{})
	if len(all) != 2 {
		t.Errorf("Everyone should rank both, got %d", len(all))
	}
}

func TestSortByMemory(t *testing.T) {
	before := at(0,
		Reading{PID: 1, CPUSeconds: 0},
		Reading{PID: 2, CPUSeconds: 0},
	)
	after := at(time.Second,
		Reading{PID: 1, CPUSeconds: 5, RSSBytes: 100},
		Reading{PID: 2, CPUSeconds: 0, RSSBytes: 900},
	)
	f := facts(proc.Process{PID: 1, Name: "busy"}, proc.Process{PID: 2, Name: "fat"})

	byCPU := Rank(before, after, f, Options{Sort: ByCPU})
	if byCPU[0].Name != "busy" {
		t.Errorf("ByCPU put %q first", byCPU[0].Name)
	}

	byMem := Rank(before, after, f, Options{Sort: ByMemory})
	if byMem[0].Name != "fat" {
		t.Errorf("ByMemory put %q first", byMem[0].Name)
	}
}

func TestLimitKeepsTheBusiest(t *testing.T) {
	var before, after []Reading
	var known []proc.Process
	for i := int32(1); i <= 20; i++ {
		before = append(before, Reading{PID: i})
		after = append(after, Reading{PID: i, CPUSeconds: float64(i)})
		known = append(known, proc.Process{PID: i})
	}

	got := Rank(at(0, before...), at(time.Second, after...), facts(known...),
		Options{Limit: 3})

	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	if got[0].PID != 20 || got[2].PID != 18 {
		t.Errorf("limit did not keep the busiest: %v %v %v", got[0].PID, got[1].PID, got[2].PID)
	}
}

// Equal rates must not shuffle between frames, or a live view flickers.
func TestTiesBreakOnPIDSoTheOrderIsStable(t *testing.T) {
	before := at(0, Reading{PID: 9}, Reading{PID: 3}, Reading{PID: 7})
	after := at(time.Second,
		Reading{PID: 9, CPUSeconds: 1},
		Reading{PID: 3, CPUSeconds: 1},
		Reading{PID: 7, CPUSeconds: 1},
	)

	got := Rank(before, after, facts(
		proc.Process{PID: 9}, proc.Process{PID: 3}, proc.Process{PID: 7},
	), Options{})

	if got[0].PID != 3 || got[1].PID != 7 || got[2].PID != 9 {
		t.Errorf("ties are not ordered by PID: %v %v %v", got[0].PID, got[1].PID, got[2].PID)
	}
}

func TestZeroElapsedYieldsNothingRatherThanInfinity(t *testing.T) {
	before := at(0, Reading{PID: 1, CPUSeconds: 0})
	after := at(0, Reading{PID: 1, CPUSeconds: 5})

	got := Rank(before, after, facts(proc.Process{PID: 1}), Options{})

	if len(got) != 0 {
		t.Errorf("want no rows when no time passed, got %+v", got)
	}
}

func TestMemoryComesFromTheLaterSnapshot(t *testing.T) {
	before := at(0, Reading{PID: 1, RSSBytes: 100})
	after := at(time.Second, Reading{PID: 1, RSSBytes: 400})

	got := Rank(before, after, facts(proc.Process{PID: 1}), Options{})

	if got[0].RSSBytes != 400 {
		t.Errorf("RSSBytes = %d, want the later reading 400", got[0].RSSBytes)
	}
}

// --- Sampler -------------------------------------------------------------

// fakeMachine counts how often each process's facts are read, which is what the
// cache exists to keep at one.
type fakeMachine struct {
	readings  []Reading
	described map[int32]int
	fail      map[int32]bool
	facts     map[int32]proc.Process
}

func (m *fakeMachine) Readings() ([]Reading, error) { return m.readings, nil }

func (m *fakeMachine) Describe(pid int32) (proc.Process, error) {
	if m.fail[pid] {
		return proc.Process{}, errors.New("gone")
	}
	if m.described == nil {
		m.described = map[int32]int{}
	}
	m.described[pid]++
	if p, ok := m.facts[pid]; ok {
		return p, nil
	}
	return proc.Process{PID: pid, Name: "p", Created: epoch}, nil
}

// Names, command lines and launch directories cost more to read than the
// measurements do and never change, so a refreshing view must not pay for them
// every frame.
func TestSamplerReadsAProcessesFactsOnce(t *testing.T) {
	m := &fakeMachine{readings: []Reading{
		{PID: 1, Created: epoch}, {PID: 2, Created: epoch},
	}}
	s := NewSampler(m)

	for range 5 {
		if _, err := s.Take(epoch); err != nil {
			t.Fatal(err)
		}
	}

	for pid, n := range m.described {
		if n != 1 {
			t.Errorf("facts for pid %d were read %d times, want 1", pid, n)
		}
	}
}

// The cache is keyed by PID, and a PID outlives the process that held it.
func TestSamplerRereadsFactsWhenAPIDIsReused(t *testing.T) {
	m := &fakeMachine{readings: []Reading{{PID: 1, Created: epoch}}}
	s := NewSampler(m)

	if _, err := s.Take(epoch); err != nil {
		t.Fatal(err)
	}
	m.readings = []Reading{{PID: 1, Created: epoch.Add(time.Hour)}}
	if _, err := s.Take(epoch.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if m.described[1] != 2 {
		t.Errorf("facts read %d times, want 2 — the second process is not the first", m.described[1])
	}
}

// A process that exits between being listed and being described is ordinary,
// not an error: the snapshot simply does not carry it.
func TestSamplerSkipsAProcessThatVanishesMidSnapshot(t *testing.T) {
	m := &fakeMachine{
		readings: []Reading{{PID: 1, Created: epoch}, {PID: 2, Created: epoch}},
		fail:     map[int32]bool{2: true},
	}
	s := NewSampler(m)

	snap, err := s.Take(epoch)
	if err != nil {
		t.Fatalf("a vanished process should not fail the snapshot: %v", err)
	}
	if len(snap.Readings) != 1 || snap.Readings[0].PID != 1 {
		t.Errorf("want only pid 1, got %+v", snap.Readings)
	}
	if _, ok := s.Facts()[2]; ok {
		t.Error("a process that could not be described should not be in the facts")
	}
}

// A live view runs for hours on a machine that churns through processes; the
// cache must not grow without bound.
func TestSamplerForgetsProcessesThatAreGone(t *testing.T) {
	m := &fakeMachine{readings: []Reading{{PID: 1, Created: epoch}, {PID: 2, Created: epoch}}}
	s := NewSampler(m)

	if _, err := s.Take(epoch); err != nil {
		t.Fatal(err)
	}
	m.readings = []Reading{{PID: 1, Created: epoch}}
	if _, err := s.Take(epoch.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.Facts()[2]; ok {
		t.Error("pid 2 is gone but its facts are still cached")
	}
}

func TestParseSortKey(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want SortKey
		bad  bool
	}{
		{"", ByCPU, false},
		{"cpu", ByCPU, false},
		{"mem", ByMemory, false},
		{"memory", ByMemory, false},
		{"rss", ByMemory, true},
		{"nonsense", ByCPU, true},
	} {
		got, err := ParseSortKey(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("ParseSortKey(%q) should have failed", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("ParseSortKey(%q) = %v, %v", tc.in, got, err)
		}
	}
}

// A rate needs two readings. The first frame of a live view has one, and saying
// nothing is the only honest thing it can do.
func TestRankerHasNothingToShowUntilItsSecondSnapshot(t *testing.T) {
	m := &fakeMachine{readings: []Reading{{PID: 1, Created: epoch, CPUSeconds: 0}}}
	r := NewRanker(m, Options{})

	first, err := r.Next(epoch)
	if err != nil {
		t.Fatal(err)
	}
	if first != nil {
		t.Errorf("first frame reported %+v, want nothing", first)
	}

	m.readings = []Reading{{PID: 1, Created: epoch, CPUSeconds: 1}}
	second, err := r.Next(epoch.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].CPUPercent != 100 {
		t.Errorf("second frame = %+v, want one row at 100%%", second)
	}
}

// --- Application ---------------------------------------------------------

// Every one of these is a real path taken from a running machine.
func TestApplication(t *testing.T) {
	for _, tc := range []struct {
		name string
		exe  string
		want string
	}{
		{
			// The bundle nests: the helper is itself a .app inside the
			// application. Taking the last one would group nothing, because
			// every helper would be its own application.
			"nested bundle takes the outermost",
			"/Applications/汽水音乐.app/Contents/Frameworks/汽水音乐 Helper (Renderer).app/Contents/MacOS/汽水音乐 Helper (Renderer)",
			"汽水音乐",
		},
		{
			"bundle name containing spaces",
			"/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper (Plugin).app/Contents/MacOS/Code Helper (Plugin)",
			"Visual Studio Code",
		},
		{
			"helper nested several levels down",
			"/Applications/Doubao.app/Contents/Helpers/Doubao Browser.app/Contents/Frameworks/Doubao Browser Framework.framework/Versions/135.0.7049.72/Helpers/Doubao Browser Helper (Renderer).app/Contents/MacOS/Doubao Browser Helper (Renderer)",
			"Doubao",
		},
		{"plain bundle", "/Applications/Warp.app/Contents/MacOS/stable", "Warp"},
		// A process outside any bundle belongs to no application. It does not
		// belong to whatever launched it: labelling `claude` as `Warp` would
		// point at the terminal when the answer is the command.
		{"command-line tool", "/opt/homebrew/bin/claude", ""},
		{"bare name", "bun", ""},
		{"empty", "", ""},
		// The executable itself, not a directory containing one — nothing is
		// grouped by this, so there is no application.
		{"path ending at the bundle", "/Users/q/Some.app", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Application(tc.exe); got != tc.want {
				t.Errorf("Application(%q) = %q, want %q", tc.exe, got, tc.want)
			}
		})
	}
}

// --- the forgotten marker ------------------------------------------------

// The process actually burning CPU is often a child of the forgotten root, so
// marking only roots would leave the row a reader is looking at unmarked.
func TestDescendantsOfAForgottenRootAreMarkedToo(t *testing.T) {
	// pid 100's process group leader (99) is dead and its parent is launchd,
	// which is the definition of forgotten; 101 is its child.
	known := facts(
		proc.Process{PID: 1, PPID: 0, PGID: 1, Name: "launchd", Cmdline: "/sbin/launchd"},
		proc.Process{PID: 100, PPID: 1, PGID: 99, Name: "bun", Cmdline: "bun /x/gbrain serve"},
		proc.Process{PID: 101, PPID: 100, PGID: 99, Name: "worker", Cmdline: "worker --busy"},
		proc.Process{PID: 200, PPID: 1, PGID: 200, Name: "tended", Cmdline: "tended"},
	)

	got := Forgotten(known)

	if !got[100] {
		t.Error("the forgotten root is not marked")
	}
	if !got[101] {
		t.Error("a descendant of a forgotten root is not marked")
	}
	if got[200] {
		t.Error("a process leading its own live group was marked")
	}
}

func TestRankerMarksForgottenRows(t *testing.T) {
	m := &fakeMachine{readings: []Reading{{PID: 100, Created: epoch}, {PID: 200, Created: epoch}}}
	m.facts = map[int32]proc.Process{
		1:   {PID: 1, PPID: 0, PGID: 1, Name: "launchd", Cmdline: "/sbin/launchd"},
		100: {PID: 100, PPID: 1, PGID: 99, Name: "bun", Cmdline: "bun /x/gbrain serve", Created: epoch},
		200: {PID: 200, PPID: 1, PGID: 200, Name: "tended", Cmdline: "tended", Created: epoch},
	}
	r := NewRanker(m, Options{})

	if _, err := r.Next(epoch); err != nil {
		t.Fatal(err)
	}
	m.readings = []Reading{
		{PID: 100, Created: epoch, CPUSeconds: 1},
		{PID: 200, Created: epoch, CPUSeconds: 2},
	}
	rows, err := r.Next(epoch.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	byPID := map[int32]Process{}
	for _, p := range rows {
		byPID[p.PID] = p
	}
	if !byPID[100].Forgotten {
		t.Error("pid 100 is a forgotten root but was not marked")
	}
	if byPID[200].Forgotten {
		t.Error("pid 200 was marked but nothing is wrong with it")
	}
}
