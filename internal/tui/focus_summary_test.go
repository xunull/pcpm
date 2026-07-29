package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xunull/pcpm/internal/top"
)

// focused returns a ranking narrowed to the given query.
func focused(t *testing.T, m TopModel, query string) TopModel {
	t.Helper()
	return press(t, typed(t, press(t, m, runes("/")), query), tea.KeyMsg{Type: tea.KeyEnter})
}

func TestNoSummaryAppearsWhenNothingIsNarrowed(t *testing.T) {
	if view := ranking(t).View(); strings.Contains(view, "matching") {
		t.Errorf("a focus summary appeared with no focus in effect:\n%s", view)
	}
}

func TestTheSummarySaysHowManyRowsTheFocusKept(t *testing.T) {
	// stubbed() has two processes; "busy" matches one of them.
	view := focused(t, ranking(t), "busy").View()

	if !strings.Contains(view, "matching 1 of 2") {
		t.Errorf("the summary does not say what the focus kept:\n%s", view)
	}
}

// The header is a statement about the machine, and the machine did not do
// anything different because a reader typed a word.
func TestTheHeaderIsUnchangedByAFocus(t *testing.T) {
	plain := ranking(t)
	head := strings.SplitN(plain.View(), "\n", 2)[0]

	narrowed := strings.SplitN(focused(t, plain, "busy").View(), "\n", 2)[0]

	if narrowed != head {
		t.Errorf("the header changed under a focus:\n before: %s\n  after: %s", head, narrowed)
	}
}

// A tall window and a short one must not disagree about what the focus kept.
func TestTheSummaryCountsEveryMatchNotTheRowsThatFit(t *testing.T) {
	tall := newTestTop(stubbedMany(30), top.FitWindow)
	tall.height = 60
	short := newTestTop(stubbedMany(30), top.FitWindow)
	short.height = 8

	tallLine := summaryLine(t, focused(t, advance(t, advance(t, tall)), "idle").View())
	shortLine := summaryLine(t, focused(t, advance(t, advance(t, short)), "idle").View())

	if tallLine != shortLine {
		t.Errorf("the summary depends on the window height:\n tall: %s\nshort: %s", tallLine, shortLine)
	}
	if !strings.Contains(tallLine, "matching 30 of 32") {
		t.Errorf("the summary counted the drawn rows rather than every match: %s", tallLine)
	}
}

// The sort key can be switched at a keystroke, so a line that only spoke of CPU
// would become half a truth the moment it was.
func TestTheSummaryReportsBothCPUAndMemory(t *testing.T) {
	line := summaryLine(t, focused(t, ranking(t), "busy").View())

	if !strings.Contains(line, "CPU") || !strings.Contains(line, "RSS") {
		t.Errorf("the summary does not report both figures: %s", line)
	}
}

func summaryLine(t *testing.T, view string) string {
	t.Helper()
	for line := range strings.SplitSeq(view, "\n") {
		if strings.HasPrefix(line, "matching ") {
			return line
		}
	}
	t.Fatalf("no focus summary in:\n%s", view)
	return ""
}
