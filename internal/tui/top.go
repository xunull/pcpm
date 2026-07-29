package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xunull/pcpm/internal/render"
	"github.com/xunull/pcpm/internal/top"
)

// legendLines is what the marker's explanation costs when any row carries it:
// the blank line above it and the line itself.
const legendLines = 2

// TopModel is the live ranking.
//
// The refresh interval and the sampling window are the same thing. Each frame
// is the difference between the snapshot just taken and the one before it, so
// what is on screen is by construction the average over exactly one interval —
// a second timer would only let the figure disagree with the period it claims.
type TopModel struct {
	ranker   *top.Ranker
	interval time.Duration
	home     string
	now      func() time.Time

	// rows is how many processes to show, or 0 to fill the terminal. A reader
	// who asked for a number gets that number; one who did not gets as much of
	// the machine as their window can hold.
	rows int

	width, height int
	frame         *top.Frame
	err           error
	sort          top.SortKey

	// focus is the narrowing in effect. It is held here rather than on the
	// frame so that it outlives the frames: a new reading arrives every
	// interval, and a focus reset by each one would last a second.
	focus top.Focus
	// editing and input are the half-typed focus. While editing, keys are text
	// rather than commands — the sort keys are letters, and a reader typing
	// "chrome" must not find themselves sorting by memory.
	editing bool
	input   string
}

// NewTop builds the live ranking. rows of top.FitWindow fills the terminal.
func NewTop(m top.Machine, opt top.Options, ignore []string, interval time.Duration, home string, rows int) TopModel {
	ranker := top.NewRanker(m, opt)
	ranker.Ignore = ignore
	return TopModel{
		ranker:   ranker,
		interval: interval,
		home:     home,
		now:      time.Now,
		rows:     rows,
		sort:     opt.Sort,
		height:   24,
		width:    80,
	}
}

type topFrameMsg struct {
	frame *top.Frame
	err   error
}

type topTickMsg time.Time

// Init takes the first snapshot straight away. It produces no frame — a rate
// needs two readings — but it starts the clock, so the first visible frame
// arrives one interval from now rather than two.
func (m TopModel) Init() tea.Cmd {
	return m.sample()
}

func (m TopModel) tick() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return topTickMsg(t) })
}

// sample runs as a command rather than inline so that reading a thousand
// processes cannot freeze the keyboard.
func (m TopModel) sample() tea.Cmd {
	ranker, now := m.ranker, m.now
	return func() tea.Msg {
		frame, err := ranker.Next(now())
		return topFrameMsg{frame: frame, err: err}
	}
}

func (m TopModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.editing {
			return m.edit(msg)
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "c":
			return m.sortBy(top.ByCPU)
		case "m":
			return m.sortBy(top.ByMemory)
		case "/":
			// Start from the focus in effect so that it can be refined. It is
			// also what makes clearing possible without a key of its own:
			// delete the text and press Enter.
			m.editing, m.input = true, m.focus.String()
			return m, nil
		}
		return m, nil

	case topTickMsg:
		return m, m.sample()

	case topFrameMsg:
		m.err = msg.err
		if msg.frame != nil {
			m.frame = msg.frame
		}
		if m.err != nil {
			return m, tea.Quit
		}
		return m, m.tick()
	}
	return m, nil
}

// edit handles a keystroke aimed at the focus being typed.
//
// Escape here means "abandon this edit", not "quit" — the reader is inside
// something, and the way out of it is not the way out of the view. Outside the
// input it still quits, which is what it has always done. Ctrl+C is the
// exception: it aborts anything, and a reader whose only reflex is Ctrl+C must
// not be trapped in a text field.
func (m TopModel) edit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEnter:
		m.editing = false
		m.focus = top.ParseFocus(m.input)
	case tea.KeyEsc:
		// The typing is discarded; the focus that was in effect is untouched.
		m.editing = false
	case tea.KeyBackspace:
		m.input = dropLastRune(m.input)
	case tea.KeyRunes, tea.KeySpace:
		m.input += string(msg.Runes)
	}
	return m, nil
}

// dropLastRune removes one character, not one byte. Slicing bytes would leave
// half a character behind for anyone whose focus is not written in ASCII.
func dropLastRune(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

// sortBy reorders what is already on screen rather than waiting for the next
// frame: the data needed is in hand, and a keystroke that appears to do nothing
// for a second reads as a keystroke that was missed.
func (m TopModel) sortBy(key top.SortKey) (tea.Model, tea.Cmd) {
	m.sort = key
	m.ranker.Options.Sort = key
	if m.frame != nil {
		reordered := *m.frame
		reordered.Rows = append([]top.Process(nil), m.frame.Rows...)
		top.Sort(reordered.Rows, key)
		m.frame = &reordered
	}
	return m, nil
}

func (m TopModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v\n", m.err)
	}
	if m.frame == nil {
		// A rate needs two readings and only one has been taken. Saying so
		// beats an empty table, which reads as "nothing is running".
		return fmt.Sprintf("measuring for %s…\n\n%s", m.interval, m.footer())
	}

	var b strings.Builder
	b.WriteString(m.head())
	b.WriteByte('\n')
	b.WriteString(render.TopTable(m.visible(), m.focus, m.home, m.width))
	b.WriteByte('\n')
	b.WriteString(m.footer())
	return b.String()
}

// head is what the machine did, and — while a Focus is narrowing the rows —
// how much of that the rows still account for.
//
// The header itself does not change under a Focus. It is a statement about the
// machine, and the machine did not do anything different because a reader typed
// a word; what changes is how much of it is on screen, which is what the line
// below it is for.
func (m TopModel) head() string {
	head := render.TopHeader(m.frame.Totals)
	if m.focus.Active() {
		// The denominators come from the frame's own totals, which is what the
		// header above was rendered from. Adding the rows up again here would
		// let the two lines disagree about the same ranking.
		head += render.FocusSummary(top.Total(m.matching()), m.frame.Totals.Ranked())
	}
	return head
}

// matching is the whole ranking as the focus leaves it — every row it keeps,
// not merely those that will fit on screen.
func (m TopModel) matching() []top.Process { return m.focus.Apply(m.frame.Rows) }

// visible is as many rows as were asked for, or as many as the window holds.
//
// The budget is measured from what the view will actually draw rather than
// counted by hand, because the header's height and the legend's presence both
// depend on the data.
func (m TopModel) visible() []top.Process {
	rows := m.matching()
	if m.rows != top.FitWindow {
		return top.Top(rows, m.rows)
	}
	fitted := top.Top(rows, max(m.height-m.chrome(false), 1))
	// The legend only appears once a marked row is on screen, and it costs two
	// lines that were not budgeted for.
	if anyForgotten(fitted) {
		fitted = top.Top(rows, max(m.height-m.chrome(true), 1))
	}
	return fitted
}

// chrome is how many lines the view spends on things that are not rows.
func (m TopModel) chrome(withLegend bool) int {
	lines := strings.Count(m.head(), "\n") + // the header, and the focus summary under it
		1 + // the blank line under it
		1 + // the column headings
		1 + // the blank line above the footer
		strings.Count(m.footer(), "\n")
	if withLegend {
		lines += legendLines
	}
	return lines
}

func anyForgotten(rows []top.Process) bool {
	for _, p := range rows {
		if p.Forgotten {
			return true
		}
	}
	return false
}

// footer is the one line always on screen, which is why the focus lives in it.
//
// A focus hides rows. A reader who has forgotten theirs is on would read a
// three-row table as the whole machine, so it is stated for as long as it is in
// effect rather than only at the moment it is set.
func (m TopModel) footer() string {
	if m.editing {
		return fmt.Sprintf("focus: /%s▏  enter apply   esc cancel\n", m.input)
	}
	cpu, mem := " c cpu ", " m memory "
	switch m.sort {
	case top.ByMemory:
		mem = "[m memory]"
	default:
		cpu = "[c cpu]"
	}
	line := fmt.Sprintf("q quit   %s %s   / focus   every %s", cpu, mem, m.interval)
	if m.focus.Active() {
		line += fmt.Sprintf("\nfocus: %s", m.focus)
	}
	return line + "\n"
}

// RunTop opens the live ranking and blocks until the reader quits.
func RunTop(ctx context.Context, m top.Machine, opt top.Options, ignore []string, interval time.Duration, home string, rows int) error {
	program := tea.NewProgram(NewTop(m, opt, ignore, interval, home, rows),
		tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}
