package watch

import (
	"errors"
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/proc"
)

// fakeMachine is a machine whose processes the test controls, so the
// collector's scheduling can be checked without depending on what is running.
type fakeMachine struct {
	procs     []proc.Process
	cpu       map[int32]float64
	snapshots int
}

func (m *fakeMachine) Snapshot() ([]proc.Process, error) {
	m.snapshots++
	return m.procs, nil
}

func (m *fakeMachine) Usage(pid int32) (Usage, error) {
	for _, p := range m.procs {
		if p.PID == pid {
			return Usage{CPUSeconds: m.cpu[pid], RSSBytes: int64(pid) << 20}, nil
		}
	}
	return Usage{}, errGone
}

var errGone = &processGoneError{}

type processGoneError struct{}

func (*processGoneError) Error() string { return "no such process" }

// collector wires a store and a fake machine with a clock the test drives.
func collector(t *testing.T, m *fakeMachine) (*Collector, *Store, *time.Time) {
	t.Helper()
	s := open(t)
	clock := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	c := NewCollector(s, m)
	c.Now = func() time.Time { return clock }
	return c, s, &clock
}

func TestTickSamplesEveryProcessInTheTree(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{
			{PID: 100, PPID: 1, Created: started},
			{PID: 101, PPID: 100, Created: started},
			{PID: 102, PPID: 101, Created: started},
			{PID: 500, PPID: 1, Created: started}, // unrelated
		},
		cpu: map[int32]float64{100: 1, 101: 2, 102: 3, 500: 99},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: started, Name: "bun"}, *clock)

	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got, _ := s.SamplesBetween(tgt.ID, clock.Add(-time.Minute), clock.Add(time.Minute))
	if len(got) != 3 {
		t.Fatalf("want the root plus 2 descendants sampled, got %d", len(got))
	}
	for _, m := range got {
		if m.PID == 500 {
			t.Error("an unrelated process was sampled")
		}
	}
}

// Each Sample describes one process, so it must carry that process's own name
// and start time — not the target root's. Getting this wrong makes the
// per-process breakdown a list of identical rows, and unpins tree members from
// the PID-reuse check.
func TestSamplesCarryEachProcessOwnIdentity(t *testing.T) {
	rootStarted := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	childStarted := rootStarted.Add(3 * time.Minute)
	m := &fakeMachine{
		procs: []proc.Process{
			{PID: 100, PPID: 1, Name: "bun", Created: rootStarted},
			{PID: 101, PPID: 100, Name: "esbuild", Created: childStarted},
		},
		cpu: map[int32]float64{100: 1, 101: 2},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: rootStarted, Name: "bun"}, *clock)

	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	got, _ := s.SamplesBetween(tgt.ID, clock.Add(-time.Minute), clock.Add(time.Minute))
	if len(got) != 2 {
		t.Fatalf("want 2 samples, got %d", len(got))
	}
	byPID := map[int32]Sample{}
	for _, sample := range got {
		byPID[sample.PID] = sample
	}
	if byPID[101].Name != "esbuild" {
		t.Errorf("the child's sample is named %q; want its own name, not the root's", byPID[101].Name)
	}
	if !byPID[101].Created.Equal(childStarted) {
		t.Errorf("the child's sample says it started %v; want its own start %v",
			byPID[101].Created, childStarted)
	}
	if byPID[100].Name != "bun" || !byPID[100].Created.Equal(rootStarted) {
		t.Errorf("the root's sample is wrong: %+v", byPID[100])
	}
}

// The daemon takes its instructions from the database, so a target added
// elsewhere must be picked up without anything telling the collector.
func TestTickPicksUpATargetAddedAfterItStarted(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{{PID: 100, PPID: 1, Created: started}},
		cpu:   map[int32]float64{100: 1},
	}
	c, s, clock := collector(t, m)

	if err := c.Tick(); err != nil { // nothing to do yet
		t.Fatalf("first Tick: %v", err)
	}

	tgt, _ := s.AddTarget(Target{PID: 100, Created: started}, *clock)
	*clock = clock.Add(5 * time.Second)
	if err := c.Tick(); err != nil {
		t.Fatalf("second Tick: %v", err)
	}

	got, _ := s.SamplesBetween(tgt.ID, started, clock.Add(time.Minute))
	if len(got) != 1 {
		t.Errorf("a target added between ticks was not picked up: %d samples", len(got))
	}
}

// Walking the process table is ~250x the cost of sampling, so it must happen on
// its own, slower schedule — not every tick.
func TestDiscoveryRunsOnItsOwnInterval(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{{PID: 100, PPID: 1, Created: started}},
		cpu:   map[int32]float64{100: 1},
	}
	c, s, clock := collector(t, m)
	c.SampleInterval = 5 * time.Second
	c.DiscoverInterval = 30 * time.Second
	s.AddTarget(Target{PID: 100, Created: started}, *clock)

	// six ticks at 5s spans 25s: discovery on the first, and not again yet
	for range 6 {
		if err := c.Tick(); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		*clock = clock.Add(5 * time.Second)
	}
	if m.snapshots != 1 {
		t.Errorf("walked the process table %d times in 25s; want 1", m.snapshots)
	}

	// crossing 30s triggers the next pass
	*clock = clock.Add(10 * time.Second)
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if m.snapshots != 2 {
		t.Errorf("discovery did not run after its interval elapsed: %d walks", m.snapshots)
	}
}

// A process that appears in the tree is picked up at the next discovery, not
// before — the deliberate cost of not walking the table every tick.
func TestANewChildIsSampledFromTheNextDiscovery(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{{PID: 100, PPID: 1, Created: started}},
		cpu:   map[int32]float64{100: 1, 101: 5},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: started}, *clock)
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// a child appears, but discovery is not due
	m.procs = append(m.procs, proc.Process{PID: 101, PPID: 100, Created: started})
	*clock = clock.Add(5 * time.Second)
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got, _ := s.SamplesBetween(tgt.ID, *clock, clock.Add(time.Second)); len(got) != 1 {
		t.Errorf("want only the root before discovery, got %d samples", len(got))
	}

	// after the discovery interval it joins the tree
	*clock = clock.Add(31 * time.Second)
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got, _ := s.SamplesBetween(tgt.ID, *clock, clock.Add(time.Second)); len(got) != 2 {
		t.Errorf("want root and child after discovery, got %d samples", len(got))
	}
}

func TestTargetWhoseProcessesHaveAllExitedIsMarkedEnded(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{{PID: 100, PPID: 1, Created: started}},
		cpu:   map[int32]float64{100: 1},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: started}, *clock)
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	m.procs = nil // everything exits
	*clock = clock.Add(31 * time.Second)
	endedAt := *clock
	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	targets, _ := s.Targets()
	if !targets[0].Ended() {
		t.Fatal("a target whose processes have all exited should be marked ended")
	}
	if !targets[0].EndedAt.Equal(endedAt) {
		t.Errorf("EndedAt = %v, want %v", *targets[0].EndedAt, endedAt)
	}
	// and its history survives
	if got, _ := s.SamplesBetween(tgt.ID, started, endedAt.Add(time.Hour)); len(got) == 0 {
		t.Error("the ended target's samples were discarded")
	}
	// and it is no longer measured
	if watched, _ := s.WatchedTargets(); len(watched) != 0 {
		t.Errorf("an ended target is still being collected: %+v", watched)
	}
}

// A PID that is reused by an unrelated process must not have that process's
// usage recorded against the old target.
func TestAReusedPIDIsNotSampledForTheOldTarget(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{{PID: 100, PPID: 1, Created: started.Add(72 * time.Hour)}},
		cpu:   map[int32]float64{100: 500},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: started}, *clock)

	if err := c.Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if got, _ := s.SamplesBetween(tgt.ID, started, clock.Add(time.Hour)); len(got) != 0 {
		t.Errorf("the recycled PID's usage was recorded against the old target: %+v", got)
	}
	targets, _ := s.Targets()
	if !targets[0].Ended() {
		t.Error("the original process is gone, so its target should be ended")
	}
}

// stubTraffic stands in for the measuring child process.
type stubTraffic struct {
	counters map[int32]Traffic
	err      error
}

func (s stubTraffic) Snapshot() map[int32]Traffic { return s.counters }
func (s stubTraffic) Err() error                  { return s.err }
func (s stubTraffic) Close() error                { return nil }

// Traffic arrives for the whole machine at once, and each tree member's figure
// is looked up in it rather than measured separately.
func TestSamplesCarryTrafficForEachProcess(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{
			{PID: 100, PPID: 1, Created: started},
			{PID: 101, PPID: 100, Created: started},
		},
		cpu: map[int32]float64{100: 1, 101: 2},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: started, Name: "bun"}, *clock)
	c.Traffic = stubTraffic{counters: map[int32]Traffic{
		100: {InBytes: 500, OutBytes: 60},
		101: {InBytes: 900, OutBytes: 10},
	}}

	if err := c.Tick(); err != nil {
		t.Fatal(err)
	}

	samples, err := s.SamplesBetween(tgt.ID, clock.Add(-time.Hour), clock.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got := map[int32]Traffic{}
	for _, sm := range samples {
		got[sm.PID] = sm.Traffic
	}
	if got[100] != (Traffic{InBytes: 500, OutBytes: 60}) {
		t.Errorf("pid 100 traffic = %+v", got[100])
	}
	if got[101] != (Traffic{InBytes: 900, OutBytes: 10}) {
		t.Errorf("pid 101 traffic = %+v", got[101])
	}
}

// A broken source must not stop CPU and memory being collected — losing one
// measurement is not a reason to lose the others.
func TestAFailedTrafficSourceDoesNotStopTheRest(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{{PID: 100, PPID: 1, Created: started}},
		cpu:   map[int32]float64{100: 7},
	}
	c, s, clock := collector(t, m)
	tgt, _ := s.AddTarget(Target{PID: 100, Created: started, Name: "bun"}, *clock)
	c.Traffic = stubTraffic{
		counters: map[int32]Traffic{100: {InBytes: 999}},
		err:      errors.New("the traffic source stopped reporting"),
	}

	if err := c.Tick(); err != nil {
		t.Fatal(err)
	}

	samples, err := s.SamplesBetween(tgt.ID, clock.Add(-time.Hour), clock.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 {
		t.Fatalf("want 1 sample, got %d", len(samples))
	}
	if samples[0].CPUSeconds != 7 {
		t.Errorf("cpu was lost along with the traffic source: %+v", samples[0])
	}
	// Figures from a source known to be broken must not be stored as if real.
	if samples[0].Traffic != (Traffic{}) {
		t.Errorf("traffic from a failed source was stored: %+v", samples[0].Traffic)
	}
}

// No source at all is the Linux case, and it must be unremarkable.
func TestCollectionWorksWithNoTrafficSource(t *testing.T) {
	started := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	m := &fakeMachine{
		procs: []proc.Process{{PID: 100, PPID: 1, Created: started}},
		cpu:   map[int32]float64{100: 3},
	}
	c, s, clock := collector(t, m)
	if _, err := s.AddTarget(Target{PID: 100, Created: started, Name: "bun"}, *clock); err != nil {
		t.Fatal(err)
	}

	if err := c.Tick(); err != nil {
		t.Fatalf("a collector with no traffic source should work: %v", err)
	}
}
