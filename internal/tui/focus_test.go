package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// press sends one key the way the runtime would.
func press(t *testing.T, m TopModel, key tea.KeyMsg) TopModel {
	t.Helper()
	next, _ := m.Update(key)
	return next.(TopModel)
}

func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typed(t *testing.T, m TopModel, s string) TopModel {
	t.Helper()
	for _, r := range s {
		key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		if r == ' ' {
			key.Type = tea.KeySpace
		}
		m = press(t, m, key)
	}
	return m
}

// ranking is a view that has taken the two readings a rate needs.
func ranking(t *testing.T) TopModel {
	t.Helper()
	return advance(t, advance(t, newTestTop(stubbed(), 0)))
}

func TestSlashOpensAPlaceToType(t *testing.T) {
	m := press(t, ranking(t), runes("/"))

	if !m.editing {
		t.Fatal("/ did not open an input")
	}
	if view := m.View(); !strings.Contains(view, "/") {
		t.Errorf("the input is not visible; a reader typing blind cannot tell they are:\n%s", view)
	}
}

func TestTypingAWordAndPressingEnterNarrowsTheTable(t *testing.T) {
	m := typed(t, press(t, ranking(t), runes("/")), "busy")
	m = press(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if !strings.Contains(view, "busy") {
		t.Errorf("the matching row is gone:\n%s", view)
	}
	if strings.Contains(view, "fat") {
		t.Errorf("a row that does not match survived the focus:\n%s", view)
	}
	if m.editing {
		t.Error("still editing after Enter")
	}
}

// A reader who has forgotten they are focused would read a two-row table as the
// whole machine. The footer is the one thing on screen that is always there.
func TestTheFooterShowsTheFocusForAsLongAsOneIsInEffect(t *testing.T) {
	m := ranking(t)
	// The key hint is always there; what must not be there is a focus value.
	if strings.Contains(m.footer(), "focus:") {
		t.Errorf("the footer states a focus when none is in effect: %q", m.footer())
	}

	m = press(t, typed(t, press(t, m, runes("/")), "busy"), tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.footer(), "busy") {
		t.Errorf("the footer does not show the focus in effect: %q", m.footer())
	}
}

func TestEscapeWhileTypingRestoresThePreviousFocus(t *testing.T) {
	m := press(t, typed(t, press(t, ranking(t), runes("/")), "busy"), tea.KeyMsg{Type: tea.KeyEnter})

	// Reopen, type something else, then think better of it.
	m = typed(t, press(t, m, runes("/")), "zzz")
	m = press(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.editing {
		t.Error("still editing after Esc")
	}
	if got := m.focus.String(); got != "busy" {
		t.Errorf("focus is %q after abandoning an edit; want the previous focus %q", got, "busy")
	}
}

// Escape means quit everywhere else in this view, and it still has to.
func TestEscapeOutsideTypingStillQuits(t *testing.T) {
	m := ranking(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Esc outside the input did nothing; want it to quit as it always has")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Errorf("Esc outside the input produced %T; want a quit", msg)
	}
}

// Ctrl+C aborts anything, including a half-typed focus. A reader whose only
// reflex is Ctrl+C should not be trapped in an input.
func TestCtrlCQuitsEvenWhileTyping(t *testing.T) {
	m := typed(t, press(t, ranking(t), runes("/")), "bu")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C while typing did nothing")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Errorf("Ctrl+C while typing produced %T; want a quit", msg)
	}
}

// Reopening pre-filled is what makes clearing possible without a key of its own:
// delete the text, press Enter.
func TestReopeningStartsFromTheFocusInEffect(t *testing.T) {
	m := press(t, typed(t, press(t, ranking(t), runes("/")), "busy"), tea.KeyMsg{Type: tea.KeyEnter})
	m = press(t, m, runes("/"))

	if m.input != "busy" {
		t.Errorf("reopened with %q; want the focus in effect so it can be refined or deleted", m.input)
	}
}

func TestDeletingTheTextAndPressingEnterClearsTheFocus(t *testing.T) {
	m := press(t, typed(t, press(t, ranking(t), runes("/")), "busy"), tea.KeyMsg{Type: tea.KeyEnter})

	m = press(t, m, runes("/"))
	for range len("busy") {
		m = press(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = press(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.focus.Active() {
		t.Errorf("focus %q survived being deleted", m.focus)
	}
	if view := m.View(); !strings.Contains(view, "fat") {
		t.Errorf("the rows a cleared focus should bring back are missing:\n%s", view)
	}
}

// Backspace has to delete a character, not a byte, or a focus typed in any
// non-ASCII script would be corrupted halfway through being erased.
func TestBackspaceDeletesAWholeCharacter(t *testing.T) {
	m := typed(t, press(t, ranking(t), runes("/")), "汽水")
	m = press(t, m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.input != "汽" {
		t.Errorf("input is %q after one backspace; want %q", m.input, "汽")
	}
}

func TestSortKeysAreTextWhileTyping(t *testing.T) {
	m := typed(t, press(t, ranking(t), runes("/")), "cm")

	if m.input != "cm" {
		t.Errorf("input is %q; want the sort keys to be letters while typing, not commands", m.input)
	}
}

// It is a live view. Freezing it while a reader types would hand back figures
// that were already stale by the time they pressed Enter.
func TestTheRankingKeepsRefreshingWhileTyping(t *testing.T) {
	m := press(t, ranking(t), runes("/"))

	next, cmd := m.Update(topTickMsg{})
	if cmd == nil {
		t.Fatal("a tick during typing scheduled no sample; the view would go stale")
	}
	after, _ := next.(TopModel).Update(cmd())
	if !after.(TopModel).editing {
		t.Error("a frame arriving mid-edit closed the input")
	}
}

func TestAFocusSurvivesTheNextFrame(t *testing.T) {
	m := press(t, typed(t, press(t, ranking(t), runes("/")), "busy"), tea.KeyMsg{Type: tea.KeyEnter})

	m = advance(t, m)

	if got := m.focus.String(); got != "busy" {
		t.Errorf("focus is %q after a new frame; want it to persist across refreshes", got)
	}
	if view := m.View(); strings.Contains(view, "fat") {
		t.Errorf("the focus stopped applying after a refresh:\n%s", view)
	}
}

// "no processes to rank" would be read as "this machine is running nothing",
// which is a different and wrong answer.
func TestAFocusThatMatchesNothingSaysSoWithoutClaimingTheMachineIsIdle(t *testing.T) {
	m := press(t, typed(t, press(t, ranking(t), runes("/")), "zzz"), tea.KeyMsg{Type: tea.KeyEnter})

	view := m.View()
	if strings.Contains(view, "no processes to rank") {
		t.Errorf("an unmatched focus claims the machine has no processes:\n%s", view)
	}
	if !strings.Contains(strings.ToLower(view), "match") {
		t.Errorf("an unmatched focus does not say that nothing matched:\n%s", view)
	}
}
