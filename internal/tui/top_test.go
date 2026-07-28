package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xunull/pcpm/internal/proc"
	"github.com/xunull/pcpm/internal/top"
)

var topEpoch = time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)

// stubMachine answers with a fixed set of processes whose counters advance by a
// known amount each time it is read.
type stubMachine struct {
	reads int
	procs []struct {
		pid  int32
		name string
		cpu  float64 // CPU seconds gained per read
		rss  int64
	}
}

func (m *stubMachine) Readings() ([]top.Reading, error) {
	m.reads++
	out := make([]top.Reading, 0, len(m.procs))
	for _, p := range m.procs {
		out = append(out, top.Reading{
			PID:        p.pid,
			Created:    topEpoch,
			CPUSeconds: p.cpu * float64(m.reads),
			RSSBytes:   p.rss,
		})
	}
	return out, nil
}

func (m *stubMachine) Describe(pid int32) (proc.Process, error) {
	for _, p := range m.procs {
		if p.pid == pid {
			return proc.Process{PID: pid, Name: p.name, Created: topEpoch}, nil
		}
	}
	return proc.Process{}, nil
}

func (m *stubMachine) System() (top.SystemReading, error) {
	return top.SystemReading{BusySeconds: float64(m.reads) * 4, Cores: 10}, nil
}

func stubbed() *stubMachine {
	m := &stubMachine{}
	m.procs = append(m.procs,
		struct {
			pid  int32
			name string
			cpu  float64
			rss  int64
		}{1, "busy", 2, 10},
		struct {
			pid  int32
			name string
			cpu  float64
			rss  int64
		}{2, "fat", 0.1, 9000},
	)
	return m
}

// stubbedMany adds idle filler so that the window, not the data, decides how
// many rows are shown. The filler sorts below both named processes on either
// key, so tests that care about the ordering are unaffected.
func stubbedMany(n int) *stubMachine {
	m := stubbed()
	for i := range n {
		m.procs = append(m.procs, struct {
			pid  int32
			name string
			cpu  float64
			rss  int64
		}{int32(100 + i), "idle", 0, 1})
	}
	return m
}

// advance drives the model through one full sample, the way the runtime would.
func advance(t *testing.T, m TopModel) TopModel {
	t.Helper()
	cmd := m.sample()
	next, _ := m.Update(cmd())
	return next.(TopModel)
}

func newTestTop(machine top.Machine, rows int) TopModel {
	m := NewTop(machine, top.Options{}, nil, time.Second, "", rows)
	clock := topEpoch
	m.now = func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	return m
}

// A rate needs two readings. Showing an empty table instead would read as
// "nothing is running", which is a different and wrong answer.
func TestLiveViewSaysWhatItIsWaitingForBeforeItsFirstFrame(t *testing.T) {
	m := newTestTop(stubbed(), 0)

	view := m.View()

	if !strings.Contains(view, "measuring") {
		t.Errorf("the first view should say what it is waiting for, got:\n%s", view)
	}
	if strings.Contains(view, "%CPU") {
		t.Errorf("a table appeared before anything had been measured:\n%s", view)
	}
}

func TestLiveViewShowsARankingOnceItHasTwoReadings(t *testing.T) {
	m := advance(t, advance(t, newTestTop(stubbed(), 0)))

	view := m.View()

	if !strings.Contains(view, "%CPU") || !strings.Contains(view, "busy") {
		t.Errorf("want a ranking, got:\n%s", view)
	}
	if strings.Contains(view, "measuring") {
		t.Errorf("still claiming to be measuring after two readings:\n%s", view)
	}
}

// The interval is the sampling window, so it must be plain what period the
// figures cover.
func TestLiveViewNamesItsInterval(t *testing.T) {
	m := advance(t, advance(t, newTestTop(stubbed(), 0)))

	if !strings.Contains(m.View(), "1s") {
		t.Errorf("the view does not say how often it refreshes:\n%s", m.View())
	}
}

func TestLiveViewQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
	} {
		m := newTestTop(stubbed(), 0)
		_, cmd := m.Update(key)
		if cmd == nil {
			t.Errorf("%v should quit", key)
			continue
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%v did not produce a quit", key)
		}
	}
}

// A keystroke that appears to do nothing for a second reads as a keystroke that
// was missed, so the reorder happens on the rows already in hand.
func TestSortKeysReorderWithoutWaitingForTheNextFrame(t *testing.T) {
	m := advance(t, advance(t, newTestTop(stubbed(), 0)))

	if first := m.frame.Rows[0].Name; first != "busy" {
		t.Fatalf("expected the CPU ordering to start with busy, got %q", first)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	byMem := next.(TopModel)
	if first := byMem.frame.Rows[0].Name; first != "fat" {
		t.Errorf("after m the first row is %q, want fat", first)
	}

	next, _ = byMem.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	byCPU := next.(TopModel)
	if first := byCPU.frame.Rows[0].Name; first != "busy" {
		t.Errorf("after c the first row is %q, want busy", first)
	}
}

// A reader who named a number gets that number, whatever the window's size.
func TestAnExplicitRowCountIsHonouredWhateverTheHeight(t *testing.T) {
	m := advance(t, advance(t, newTestTop(stubbed(), 1)))
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
	m = sized.(TopModel)

	if got := len(m.visible()); got != 1 {
		t.Errorf("showing %d rows, want the 1 that was asked for", got)
	}
}

// A reader who named no number gets as much of the machine as the window holds,
// which changes when the window does.
func TestRowCountFollowsTheWindowWhenNoneWasAskedFor(t *testing.T) {
	m := advance(t, advance(t, newTestTop(stubbedMany(40), 0)))

	tall, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
	short, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})

	if len(tall.(TopModel).visible()) <= len(short.(TopModel).visible()) {
		t.Errorf("a taller window did not show more rows: %d vs %d",
			len(tall.(TopModel).visible()), len(short.(TopModel).visible()))
	}
	if got := len(short.(TopModel).visible()); got < 1 {
		t.Errorf("a very short window showed %d rows, want at least 1", got)
	}
}

// The table is drawn to the terminal's width, so a resize has to reach it.
func TestResizeReachesTheTable(t *testing.T) {
	m := advance(t, advance(t, newTestTop(stubbed(), 0)))
	narrowed, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})

	for _, line := range strings.Split(narrowed.(TopModel).View(), "\n") {
		if strings.Contains(line, "%CPU") || strings.Contains(line, "busy") {
			if len([]rune(line)) > 40 {
				t.Errorf("a table line is %d columns in a 40-column window: %q", len([]rune(line)), line)
			}
		}
	}
}

// Every frame after the first reads the counters once. Re-reading the names and
// launch directories each time would cost more than the measurements do.
func TestEachFrameCostsOneReadOfTheMachine(t *testing.T) {
	machine := stubbed()
	m := newTestTop(machine, 0)

	for range 4 {
		m = advance(t, m)
	}

	if machine.reads != 4 {
		t.Errorf("the machine was read %d times for 4 frames", machine.reads)
	}
}

// A view that draws more lines than the window has scrolls its own header off
// the top. The budget must match what is actually drawn, in both the plain case
// and the one where the marker legend appears.
func TestTheViewNeverDrawsMoreLinesThanTheWindowHolds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		forgotten bool
	}{
		{"no legend", false},
		{"with the marker legend", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := advance(t, advance(t, newTestTop(stubbedMany(40), 0)))
			if tc.forgotten {
				for i := range m.frame.Rows {
					m.frame.Rows[i].Forgotten = true
				}
			}
			for _, height := range []int{10, 24, 40} {
				sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: height})
				view := sized.(TopModel)
				drawn := len(strings.Split(strings.TrimSuffix(view.View(), "\n"), "\n"))
				if drawn > height {
					t.Errorf("height %d: the view drew %d lines", height, drawn)
				}
			}
		})
	}
}

// The view has to keep refreshing on its own, not stop after the first frame.
func TestAFrameSchedulesTheNextTickAndATickSamplesAgain(t *testing.T) {
	machine := stubbed()
	m := newTestTop(machine, 0)

	// a completed sample must schedule the next tick
	next, cmd := m.Update(topFrameMsg{frame: nil})
	if cmd == nil {
		t.Fatal("a frame scheduled nothing; the view would stop refreshing")
	}
	if _, ok := cmd().(topTickMsg); !ok {
		t.Errorf("a frame scheduled %T, want a tick", cmd())
	}

	// and a tick must sample again
	before := machine.reads
	_, cmd = next.(TopModel).Update(topTickMsg(topEpoch))
	if cmd == nil {
		t.Fatal("a tick produced no command")
	}
	if _, ok := cmd().(topFrameMsg); !ok {
		t.Error("a tick did not produce a frame")
	}
	if machine.reads == before {
		t.Error("a tick did not read the machine")
	}
}

// An error has nowhere to go in a full-screen view, so it must end the program
// rather than be redrawn forever.
func TestAFailedReadEndsTheView(t *testing.T) {
	m := newTestTop(stubbed(), 0)

	_, cmd := m.Update(topFrameMsg{err: errFailedRead})
	if cmd == nil {
		t.Fatal("a read error produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("a read error did not end the view")
	}
}

var errFailedRead = errors.New("cannot read processes")
