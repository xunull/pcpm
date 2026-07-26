package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xunull/pcpm/internal/watch"
)

// fakeSource serves fixed data and records what was asked of it, so the view's
// behaviour can be checked without a database.
type fakeSource struct {
	status   watch.Status
	points   []watch.Point
	summary  watch.Summary
	err      error
	requests []time.Duration // the span of each Series call
}

func (f *fakeSource) Status() (watch.Status, error) { return f.status, f.err }

func (f *fakeSource) Series(from, to time.Time, _ time.Duration) ([]watch.Point, error) {
	f.requests = append(f.requests, to.Sub(from))
	return f.points, f.err
}

func (f *fakeSource) Summary(time.Time, time.Time, time.Duration) (watch.Summary, error) {
	return f.summary, f.err
}

func source() *fakeSource {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	return &fakeSource{
		status: watch.Status{
			Target:  watch.Target{PID: 100, Name: "bun", Cmdline: "bun run dev", Cwd: "/proj"},
			Running: true,
		},
		points: []watch.Point{
			{At: now.Add(-30 * time.Minute), CPUPercent: 20, RSSBytes: 300 << 20, Procs: 2},
			{At: now.Add(-15 * time.Minute), CPUPercent: 90, RSSBytes: 400 << 20, Procs: 2},
		},
		summary: watch.Summary{
			CurrentCPUPercent: 20, PeakCPUPercent: 90,
			CurrentRSSBytes: 300 << 20, PeakRSSBytes: 400 << 20,
			Samples: 100,
			Processes: []watch.ProcessUsage{
				{PID: 101, Name: "esbuild", CPUPercent: 18, RSSBytes: 280 << 20},
				{PID: 100, Name: "bun", CPUPercent: 2, RSSBytes: 20 << 20},
			},
		},
	}
}

// runCmd executes a command the way the bubbletea runtime does, following
// batches into their members rather than stopping at the BatchMsg.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, runCmd(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// loaded drives a model through its first load, as the runtime would.
func loaded(t *testing.T, f *fakeSource) Model {
	t.Helper()
	m := New(f, "", 1)
	m.now = func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) }
	m.refresh = time.Millisecond
	m.width, m.height = 100, 40

	cmd := m.load()
	next, _ := m.Update(cmd())
	return next.(Model)
}

func TestViewShowsBothChartsAndTheProcessList(t *testing.T) {
	m := loaded(t, source())
	out := m.View()

	for _, want := range []string{"cpu", "memory", "bun", "esbuild", "PID", "NAME"} {
		if !strings.Contains(out, want) {
			t.Errorf("view is missing %q:\n%s", want, out)
		}
	}
	// the tree's own root is not necessarily the busy one, so both must show
	if !strings.Contains(out, "101") || !strings.Contains(out, "100") {
		t.Errorf("both processes should be listed:\n%s", out)
	}
}

func TestSwitchingWindowRequeries(t *testing.T) {
	f := source()
	m := loaded(t, f)
	before := len(f.requests)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if cmd == nil {
		t.Fatal("switching window should trigger a reload")
	}
	cmd() // run the command the runtime would

	m = next.(Model)
	if Windows[m.window].Label != "24h" {
		t.Errorf("window = %s, want 24h", Windows[m.window].Label)
	}
	if len(f.requests) != before+1 {
		t.Fatalf("want one more query after switching, got %d", len(f.requests)-before)
	}
	if got := f.requests[len(f.requests)-1]; got != 24*time.Hour {
		t.Errorf("queried a span of %s, want 24h", got)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
	} {
		m := loaded(t, source())
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

// The view refreshes on its own so it can be left open, and must keep
// refreshing rather than stopping after the first tick.
func TestTickReloadsAndSchedulesTheNextOne(t *testing.T) {
	f := source()
	m := loaded(t, f)
	before := len(f.requests)

	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("a tick should reload")
	}
	msgs := runCmd(cmd)

	if len(f.requests) <= before {
		t.Error("the tick did not re-query")
	}
	// and it must arm the next one, or the view refreshes exactly once
	scheduled := false
	for _, msg := range msgs {
		if _, ok := msg.(tickMsg); ok {
			scheduled = true
		}
	}
	if !scheduled {
		t.Error("the tick did not schedule the next refresh; the view would go stale")
	}
}

func TestGapIsExplainedRatherThanLeftAsAMysteriousBreak(t *testing.T) {
	f := source()
	f.points[1].Gap = true
	m := loaded(t, f)

	if !strings.Contains(m.View(), "no data") {
		t.Errorf("a window containing a gap should say what the break means:\n%s", m.View())
	}
}

func TestErrorsAreShownRatherThanSwallowed(t *testing.T) {
	f := source()
	f.err = errors.New("database is locked")
	m := New(f, "", 1)
	m.width, m.height = 100, 40

	next, _ := m.Update(m.load()())
	out := next.(Model).View()

	if !strings.Contains(out, "database is locked") {
		t.Errorf("the failure should be visible:\n%s", out)
	}
}

// A short terminal must still show both charts and some of the process list.
func TestChartsShrinkToFitAShortTerminal(t *testing.T) {
	m := loaded(t, source())
	m.width, m.height = 80, 20

	if h := m.chartHeight(); h < 3 {
		t.Errorf("chart height %d is unusable", h)
	}
	out := m.View()
	if !strings.Contains(out, "cpu") || !strings.Contains(out, "memory") {
		t.Errorf("both charts should survive a short terminal:\n%s", out)
	}
	if lines := strings.Count(out, "\n"); lines > 40 {
		t.Errorf("view is %d lines on a 20-line terminal", lines)
	}
}

func TestEndedTargetIsLabelledAsSuch(t *testing.T) {
	f := source()
	f.status.Running = false
	m := loaded(t, f)

	if !strings.Contains(m.View(), "ended") {
		t.Errorf("a target whose processes have exited should say so:\n%s", m.View())
	}
}
