// Package tui is the terminal view of a Watch Target: two charts, the tree's
// process list, and keys to change the window. The time axis is fixed per
// window — there is no zoom or pan — so the charts favour reading the current
// value at a glance over showing detail there is no way to magnify.
package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xunull/pcpm/internal/render"
	"github.com/xunull/pcpm/internal/watch"
)

// Window is one of the fixed spans the view can show.
type Window struct {
	Key    string
	Label  string
	Span   time.Duration
	Bucket time.Duration
}

// Windows are the spans offered, from "what is it doing right now" to "has this
// been creeping up all week".
var Windows = []Window{
	{Key: "1", Label: "5m", Span: 5 * time.Minute, Bucket: 5 * time.Second},
	{Key: "2", Label: "1h", Span: time.Hour, Bucket: 30 * time.Second},
	{Key: "3", Label: "24h", Span: 24 * time.Hour, Bucket: 10 * time.Minute},
	{Key: "4", Label: "7d", Span: 7 * 24 * time.Hour, Bucket: time.Hour},
}

// refreshInterval is how often the view re-queries. The collector samples every
// five seconds by default, so anything faster only redraws the same numbers.
const refreshInterval = 5 * time.Second

// warnCPUPercent is where the CPU line turns red: roughly one core saturated.
const warnCPUPercent = 80

// Source is where the view gets its data. It is an interface so the model can
// be exercised without a database.
type Source interface {
	Status() (watch.Status, error)
	Series(from, to time.Time, bucket time.Duration) ([]watch.Point, error)
	// SeriesOfProcess narrows the same query to one member of the tree, so its
	// own contribution can be shown against the total.
	SeriesOfProcess(pid int32, from, to time.Time, bucket time.Duration) ([]watch.Point, error)
	Summary(from, to time.Time, bucket time.Duration) (watch.Summary, error)
}

// Model is the view's state.
type Model struct {
	source Source
	home   string
	now    func() time.Time
	// refresh is how often the view re-queries, injectable so tests need not
	// wait out a real timer.
	refresh time.Duration

	window  int
	width   int
	height  int
	status  watch.Status
	points  []watch.Point
	summary watch.Summary
	err     error
	loaded  bool

	// selected is an index into summary.Processes, or -1 for "the whole tree".
	// The selected process's own line is drawn against the total, because the
	// command you recognise is usually a wrapper and the question is which part
	// of the tree is responsible.
	selected     int
	selectedPID  int32
	selectedLine []watch.Point
}

// New returns a view of one Watch Target, opening on the given window.
func New(source Source, home string, window int) Model {
	if window < 0 || window >= len(Windows) {
		window = 1
	}
	return Model{
		source:   source,
		home:     home,
		now:      time.Now,
		refresh:  refreshInterval,
		window:   window,
		selected: -1,
		width:    80,
		height:   24,
	}
}

// loadedMsg carries one refresh's worth of data.
type loadedMsg struct {
	status   watch.Status
	points   []watch.Point
	summary  watch.Summary
	selected []watch.Point
	err      error
}

type tickMsg time.Time

// Init starts the first load and the refresh timer.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.tick())
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// load fetches the current window. It runs as a command rather than inline so a
// slow query cannot freeze the keyboard.
func (m Model) load() tea.Cmd {
	w := Windows[m.window]
	to := m.now()
	from := to.Add(-w.Span)
	pid := m.selectedPID
	return func() tea.Msg {
		status, err := m.source.Status()
		if err != nil {
			return loadedMsg{err: err}
		}
		points, err := m.source.Series(from, to, w.Bucket)
		if err != nil {
			return loadedMsg{err: err}
		}
		summary, err := m.source.Summary(from, to, w.Bucket)
		if err != nil {
			return loadedMsg{err: err}
		}
		var selected []watch.Point
		if pid != 0 {
			if selected, err = m.source.SeriesOfProcess(pid, from, to, w.Bucket); err != nil {
				return loadedMsg{err: err}
			}
		}
		return loadedMsg{status: status, points: points, summary: summary, selected: selected}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch key := msg.String(); key {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, m.load()
		case "tab", "down", "j":
			return m.selectNext(1)
		case "shift+tab", "up", "k":
			return m.selectNext(-1)
		default:
			for i, w := range Windows {
				if key == w.Key {
					m.window = i
					return m, m.load()
				}
			}
		}
		return m, nil

	case loadedMsg:
		m.loaded = true
		m.err = msg.err
		if msg.err == nil {
			m.status, m.points, m.summary = msg.status, msg.points, msg.summary
			m.selectedLine = msg.selected
			// The breakdown is reordered on every refresh, so follow the PID
			// rather than the row: otherwise the selection slides onto whatever
			// process happens to be busiest now.
			m.selected = indexOfPID(m.summary.Processes, m.selectedPID)
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.load(), m.tick())
	}
	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("pcpm: %v\n\npress q to quit\n", m.err)
	}
	if !m.loaded {
		return "loading…\n"
	}

	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")

	w := Windows[m.window]
	to := m.now()
	from := to.Add(-w.Span)
	chartWidth := max(m.width-12, 20)
	chartHeight := m.chartHeight()

	b.WriteString(render.Chart(m.points, render.CPUValue, render.ChartOptions{
		Width: chartWidth, Height: chartHeight, From: from, To: to,
		Caption:   m.cpuCaption(),
		Label:     render.Percent,
		WarnAbove: warnCPUPercent,
		Overlay:   m.selectedLine,
	}))
	b.WriteString("\n")
	b.WriteString(render.Chart(m.points, render.RSSValue, render.ChartOptions{
		Width: chartWidth, Height: chartHeight, From: from, To: to,
		Caption: fmt.Sprintf("memory — now %s, peak %s", render.Bytes(m.summary.CurrentRSSBytes), render.Bytes(m.summary.PeakRSSBytes)),
		Label:   func(v float64) string { return render.Bytes(int64(v)) },
	}))
	b.WriteString("\n")
	b.WriteString(m.processes())
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

// cpuCaption says what the chart is showing, including which process the second
// line belongs to when one is selected.
func (m Model) cpuCaption() string {
	base := fmt.Sprintf("cpu — now %s, peak %s",
		render.Percent(m.summary.CurrentCPUPercent), render.Percent(m.summary.PeakCPUPercent))
	if m.selected >= 0 && m.selected < len(m.summary.Processes) {
		p := m.summary.Processes[m.selected]
		return fmt.Sprintf("%s   ·   tree total, and %s (%d) alone", base, p.Name, p.PID)
	}
	return base
}

// selectNext moves the selection through the process list, wrapping back to
// "the whole tree" at either end.
func (m Model) selectNext(step int) (tea.Model, tea.Cmd) {
	n := len(m.summary.Processes)
	if n == 0 {
		return m, nil
	}
	next := m.selected + step
	switch {
	case next >= n, next < -1:
		next = -1
	}
	m.selected = next
	if next < 0 {
		m.selectedPID, m.selectedLine = 0, nil
		return m, nil
	}
	m.selectedPID = m.summary.Processes[next].PID
	return m, m.load()
}

// indexOfPID finds a process in a breakdown, or -1.
func indexOfPID(usage []watch.ProcessUsage, pid int32) int {
	if pid == 0 {
		return -1
	}
	for i, u := range usage {
		if u.PID == pid {
			return i
		}
	}
	return -1
}

// header names the target and says what became of it.
func (m Model) header() string {
	state := "running"
	switch {
	case !m.status.Watching():
		state = "not watching"
	case !m.status.Running:
		state = "ended"
	}
	line := fmt.Sprintf("%d · %s · %s", m.status.PID, m.status.Name, state)
	if dir := render.ShortPath(m.status.Cwd, m.home, 40); dir != "" {
		line += " · " + dir
	}
	return truncateLine(line, m.width) + "\n"
}

// processes lists the tree, busiest first — the point of looking is to find
// what is actually doing the work, which is often not the root.
func (m Model) processes() string {
	if len(m.summary.Processes) == 0 {
		return "no processes sampled in this window\n"
	}
	limit := min(len(m.summary.Processes), m.processRows())
	rows := make([][]string, limit)
	for i, p := range m.summary.Processes[:limit] {
		marker := " "
		if i == m.selected {
			marker = "▸"
		}
		rows[i] = []string{
			marker,
			strconv.Itoa(int(p.PID)),
			p.Name,
			render.Percent(p.CPUPercent),
			render.Bytes(p.RSSBytes),
		}
	}
	out := render.Grid([]string{" ", "PID", "NAME", "CPU", "RSS"}, rows, m.width)
	if hidden := len(m.summary.Processes) - limit; hidden > 0 {
		out += fmt.Sprintf("… and %d more\n", hidden)
	}
	return out
}

// footer shows the window keys, with the current one marked.
func (m Model) footer() string {
	var parts []string
	for i, w := range Windows {
		mark := " "
		if i == m.window {
			mark = "*"
		}
		parts = append(parts, fmt.Sprintf("[%s]%s%s", w.Key, w.Label, mark))
	}
	gap := ""
	if m.hasGap() {
		gap = "  · breaks mean no data was collected"
	}
	return truncateLine(strings.Join(parts, " ")+"   [tab]process [r]refresh [q]quit"+gap, m.width) + "\n"
}

// hasGap reports whether the window contains a period nothing was collected in.
func (m Model) hasGap() bool {
	for _, p := range m.points {
		if p.Gap {
			return true
		}
	}
	return false
}

// chartHeight divides what is left after the fixed furniture between the two
// charts, so a short terminal still shows both.
func (m Model) chartHeight() int {
	const furniture = 12 // header, captions, axes, process header, footer
	h := (m.height - furniture - m.processRows()) / 2
	return max(min(h, 14), 3)
}

// processRows is how many processes the list may show.
func (m Model) processRows() int {
	if m.height < 24 {
		return 3
	}
	return 6
}

func truncateLine(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	return s[:width]
}
