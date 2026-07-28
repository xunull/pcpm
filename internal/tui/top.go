package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "c":
			return m.sortBy(top.ByCPU)
		case "m":
			return m.sortBy(top.ByMemory)
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
	b.WriteString(render.TopHeader(m.frame.Totals))
	b.WriteByte('\n')
	b.WriteString(render.TopTable(m.visible(), m.home, m.width))
	b.WriteByte('\n')
	b.WriteString(m.footer())
	return b.String()
}

// visible is as many rows as were asked for, or as many as the window holds.
//
// The budget is measured from what the view will actually draw rather than
// counted by hand, because the header's height and the legend's presence both
// depend on the data.
func (m TopModel) visible() []top.Process {
	if m.rows != top.FitWindow {
		return top.Top(m.frame.Rows, m.rows)
	}
	rows := top.Top(m.frame.Rows, max(m.height-m.chrome(false), 1))
	// The legend only appears once a marked row is on screen, and it costs two
	// lines that were not budgeted for.
	if anyForgotten(rows) {
		rows = top.Top(m.frame.Rows, max(m.height-m.chrome(true), 1))
	}
	return rows
}

// chrome is how many lines the view spends on things that are not rows.
func (m TopModel) chrome(withLegend bool) int {
	lines := strings.Count(render.TopHeader(m.frame.Totals), "\n") + // the header
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

func (m TopModel) footer() string {
	cpu, mem := " c cpu ", " m memory "
	switch m.sort {
	case top.ByMemory:
		mem = "[m memory]"
	default:
		cpu = "[c cpu]"
	}
	return fmt.Sprintf("q quit   %s %s   every %s\n", cpu, mem, m.interval)
}

// RunTop opens the live ranking and blocks until the reader quits.
func RunTop(ctx context.Context, m top.Machine, opt top.Options, ignore []string, interval time.Duration, home string, rows int) error {
	program := tea.NewProgram(NewTop(m, opt, ignore, interval, home, rows),
		tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}
