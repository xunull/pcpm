// Package render turns pcpm's findings into human- and machine-readable output.
package render

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xunull/pcpm/internal/forgotten"
	"github.com/xunull/pcpm/internal/listen"
	"github.com/xunull/pcpm/internal/watch"
)

// ellipsis marks a value that has been cut short to fit its column.
const ellipsis = "…"

// Format selects how a command renders its findings.
type Format int

const (
	FormatTable Format = iota // aligned human-readable table (the default)
	FormatJSON                // structured JSON array
)

// ParseFormat maps an --output value to a Format. The empty string means the
// default (table). Any other value is an error naming the valid choices.
func ParseFormat(s string) (Format, error) {
	switch s {
	case "", "table":
		return FormatTable, nil
	case "json":
		return FormatJSON, nil
	default:
		return FormatTable, fmt.Errorf("invalid output format %q: want \"table\" or \"json\"", s)
	}
}

// Age renders how long a process has been running as a compact two-unit string
// like "3d4h", "6h12m", "45m", or "30s". A created time in the future (clock
// skew) clamps to "0s"; an unknown created time (zero value) renders "?".
func Age(now, created time.Time) string {
	if created.IsZero() {
		return "?"
	}
	d := max(now.Sub(created), 0)
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	mins := int((d % time.Hour) / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm", mins)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// ShortPath renders a filesystem path for display: home becomes "~", and a path
// still longer than maxLen collapses to its last two segments behind "…/".
func ShortPath(path, home string, maxLen int) string {
	if path == "" {
		return ""
	}
	if home = strings.TrimSuffix(home, "/"); home != "" {
		// Match on a path boundary, so /Users/melissa is not read as ~lissa.
		switch {
		case path == home:
			path = "~"
		case strings.HasPrefix(path, home+"/"):
			path = "~" + strings.TrimPrefix(path, home)
		}
	}
	if len(path) <= maxLen {
		return path
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) <= 2 {
		return path
	}
	return ellipsis + "/" + strings.Join(segments[len(segments)-2:], "/")
}

// encodeJSON marshals v as an indented JSON document. HTML escaping is off so
// that &, <, > in cmdlines stay literal (jq- and human-friendly).
func encodeJSON(v any) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return b.String(), nil
}

// jsonForgotten is the machine-readable view of a forgotten process tree: every
// field, none truncated, plus the tree's size and ports. CreateTime is RFC 3339,
// or empty when the process start time is unknown.
type jsonForgotten struct {
	PID        int32      `json:"pid"`
	PPID       int32      `json:"ppid"`
	PGID       int32      `json:"pgid"`
	UID        int32      `json:"uid"`
	User       string     `json:"user"`
	Name       string     `json:"name"`
	Cmdline    string     `json:"cmdline"`
	Cwd        string     `json:"cwd"`
	CreateTime string     `json:"create_time"`
	Procs      int        `json:"procs"`
	Ports      []jsonPort `json:"ports"`
}

// ForgottenJSON renders forgotten process trees as an indented JSON array with
// all fields untruncated. No trees renders "[]" (never "null").
func ForgottenJSON(trees []forgotten.Tree) (string, error) {
	views := make([]jsonForgotten, len(trees))
	for i, t := range trees {
		v := jsonForgotten{
			PID:     t.Root.PID,
			PPID:    t.Root.PPID,
			PGID:    t.Root.PGID,
			UID:     t.Root.UID,
			User:    t.Root.User,
			Name:    t.Root.Name,
			Cmdline: t.Root.Cmdline,
			Cwd:     t.Root.Cwd,
			Procs:   t.Procs,
			Ports:   make([]jsonPort, len(t.Ports)),
		}
		if !t.Root.Created.IsZero() {
			v.CreateTime = t.Root.Created.UTC().Format(time.RFC3339)
		}
		for j, p := range t.Ports {
			v.Ports[j] = jsonPort{Port: p.Number, Exposed: p.Exposed}
		}
		views[i] = v
	}
	return encodeJSON(views)
}

// jsonPort is the machine-readable view of a listening port.
type jsonPort struct {
	Port    uint32 `json:"port"`
	Exposed bool   `json:"exposed"`
}

// jsonListener is the machine-readable view of a Listener: every field, and its
// ports as an array. CreateTime is RFC 3339, or empty when unknown.
type jsonListener struct {
	PID        int32      `json:"pid"`
	UID        int32      `json:"uid"`
	User       string     `json:"user"`
	Name       string     `json:"name"`
	Cmdline    string     `json:"cmdline"`
	CreateTime string     `json:"create_time"`
	Ports      []jsonPort `json:"ports"`
}

// ListenersJSON renders listeners as an indented JSON array with all fields
// untruncated and a ports array per process. No listeners renders "[]".
func ListenersJSON(ls []listen.Listener) (string, error) {
	views := make([]jsonListener, len(ls))
	for i, l := range ls {
		v := jsonListener{
			PID:     l.PID,
			UID:     l.UID,
			User:    l.User,
			Name:    l.Name,
			Cmdline: l.Cmdline,
			Ports:   make([]jsonPort, len(l.Ports)),
		}
		if !l.Created.IsZero() {
			v.CreateTime = l.Created.UTC().Format(time.RFC3339)
		}
		for j, p := range l.Ports {
			v.Ports[j] = jsonPort{Port: p.Number, Exposed: p.Exposed}
		}
		views[i] = v
	}
	return encodeJSON(views)
}

// Grid renders rows as an aligned text table under the given headers. Every
// column but the last is padded to its widest cell; the last column is
// truncated so each line fits within width (width <= 0 disables truncation).
// A row may have fewer cells than there are headers; missing cells render empty.
func Grid(header []string, rows [][]string, width int) string {
	cols := len(header)
	w := make([]int, cols)
	for i, h := range header {
		w[i] = len(h)
	}
	for _, r := range rows {
		for i := 0; i < cols-1 && i < len(r); i++ { // last column is never padded
			w[i] = max(w[i], len(r[i]))
		}
	}
	at := func(r []string, i int) string {
		if i < len(r) {
			return r[i]
		}
		return ""
	}

	var b strings.Builder
	writeRow := func(r []string, isHeader bool) {
		var prefix strings.Builder
		for i := 0; i < cols-1; i++ {
			fmt.Fprintf(&prefix, "%-*s  ", w[i], at(r, i))
		}
		last := at(r, cols-1)
		if width > 0 {
			room := max(0, width-prefix.Len())
			if isHeader {
				// A header is a fixed label, not free-form text: eliding its
				// middle turns COMMAND into "C…AND", which reads as neither.
				last = truncate(last, room)
			} else {
				// Every table puts its free-form value last, and in each of
				// them that value is a command line. Anything short enough to
				// fit — an RSS figure, a percentage — passes through untouched.
				last = fitCommand(last, room)
			}
		}
		b.WriteString(strings.TrimRight(prefix.String()+last, " "))
		b.WriteByte('\n')
	}

	writeRow(header, true)
	for _, r := range rows {
		writeRow(r, false)
	}
	return b.String()
}

// dirColumnWidth caps the launch-directory column so it doesn't crowd out the
// command; longer paths collapse to their last two segments (see ShortPath).
const dirColumnWidth = 32

// ForgottenTable renders forgotten process trees as an aligned table with
// columns PID / PGID / AGE / PORTS / PROCS / DIR / COMMAND, in the order
// supplied. A tree that listens on nothing shows "-" for PORTS, as does an
// unknown launch directory for DIR.
//
// PGID is shown because it, not PID, is what cleaning up the whole tree needs
// (`kill -- -<PGID>`); the two differ, and reaching for PID is the natural
// mistake.
func ForgottenTable(trees []forgotten.Tree, now time.Time, home string, width int) string {
	rows := make([][]string, len(trees))
	for i, t := range trees {
		ports := formatPorts(t.Ports)
		if ports == "" {
			ports = "-"
		}
		dir := ShortPath(t.Root.Cwd, home, dirColumnWidth)
		if dir == "" {
			dir = "-"
		}
		rows[i] = []string{
			strconv.Itoa(int(t.Root.PID)),
			strconv.Itoa(int(t.Root.PGID)),
			Age(now, t.Root.Created),
			ports,
			strconv.Itoa(t.Procs),
			dir,
			t.Root.Cmdline,
		}
	}
	return Grid([]string{"PID", "PGID", "AGE", "PORTS", "PROCS", "DIR", "COMMAND"}, rows, width)
}

// Bytes renders a byte count the way a person reads memory: three significant
// figures at most, in the largest unit that keeps the number above 1.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, exp := float64(n), 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}
	suffix := [...]string{"B", "KB", "MB", "GB", "TB"}[exp]
	if value < 10 {
		return fmt.Sprintf("%.1f %s", value, suffix)
	}
	return fmt.Sprintf("%.0f %s", value, suffix)
}

// Percent renders a CPU figure. Values are unbounded above 100 — a tree can use
// more than one core — so the width is not fixed.
func Percent(p float64) string {
	if p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// WatchSummaryText renders a Watch Target's history as a short human report:
// what it is, whether it is still there, and what it has been consuming — with
// the per-process breakdown that says which part of the tree is responsible.
func WatchSummaryText(s watch.Status, sum watch.Summary, window time.Duration, now time.Time, home string, width int) string {
	var b strings.Builder

	dir := ShortPath(s.Cwd, home, dirColumnWidth)
	fmt.Fprintf(&b, "%s\n", fit(s.Cmdline, width))
	if dir != "" {
		fmt.Fprintf(&b, "%s\n", dir)
	}
	fmt.Fprintf(&b, "%s · %s · added %s ago\n\n",
		watchingLabel(s.Watching()), processLabel(s), Age(now, s.AddedAt))

	if sum.Samples == 0 {
		fmt.Fprintf(&b, "no samples in the last %s — is the collector running? (pcpm watch daemon)\n", window)
		return b.String()
	}

	fmt.Fprintf(&b, "window   last %-14s samples %d over %s\n",
		window, sum.Samples, Age(sum.Last, sum.First))
	fmt.Fprintf(&b, "cpu      %-14s peak %s\n", Percent(sum.CurrentCPUPercent), Percent(sum.PeakCPUPercent))
	fmt.Fprintf(&b, "memory   %-14s peak %s\n\n", Bytes(sum.CurrentRSSBytes), Bytes(sum.PeakRSSBytes))

	rows := make([][]string, len(sum.Processes))
	for i, p := range sum.Processes {
		rows[i] = []string{
			strconv.Itoa(int(p.PID)),
			p.Name,
			Percent(p.CPUPercent),
			Bytes(p.RSSBytes),
		}
	}
	b.WriteString(Grid([]string{"PID", "NAME", "CPU", "RSS"}, rows, width))
	return b.String()
}

// watchingLabel describes whether pcpm is still collecting.
func watchingLabel(watching bool) string {
	if watching {
		return "watching"
	}
	return "not watching"
}

// processLabel describes what became of the target's processes.
func processLabel(s watch.Status) string {
	if s.Running {
		return "running"
	}
	if s.EndedAt != nil {
		return "ended"
	}
	return "gone"
}

// WatchSummaryJSON renders a Watch Target's history as a JSON object, with the
// per-process breakdown included.
func WatchSummaryJSON(s watch.Status, sum watch.Summary, window time.Duration) (string, error) {
	view := struct {
		Target     jsonWatchTarget    `json:"target"`
		WindowSecs float64            `json:"window_seconds"`
		Samples    int                `json:"samples"`
		First      string             `json:"first_sample"`
		Last       string             `json:"last_sample"`
		CPUPercent float64            `json:"cpu_percent"`
		PeakCPU    float64            `json:"peak_cpu_percent"`
		RSSBytes   int64              `json:"rss_bytes"`
		PeakRSS    int64              `json:"peak_rss_bytes"`
		Processes  []jsonProcessUsage `json:"processes"`
	}{
		Target:     watchTargetView(s),
		WindowSecs: window.Seconds(),
		Samples:    sum.Samples,
		CPUPercent: sum.CurrentCPUPercent,
		PeakCPU:    sum.PeakCPUPercent,
		RSSBytes:   sum.CurrentRSSBytes,
		PeakRSS:    sum.PeakRSSBytes,
		Processes:  make([]jsonProcessUsage, len(sum.Processes)),
	}
	if !sum.First.IsZero() {
		view.First = sum.First.UTC().Format(time.RFC3339)
		view.Last = sum.Last.UTC().Format(time.RFC3339)
	}
	for i, p := range sum.Processes {
		view.Processes[i] = jsonProcessUsage{
			PID: p.PID, Name: p.Name, CPUPercent: p.CPUPercent, RSSBytes: p.RSSBytes,
		}
	}
	return encodeJSON(view)
}

// jsonProcessUsage is one process's share of a target over the window.
type jsonProcessUsage struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSBytes   int64   `json:"rss_bytes"`
}

// WatchTargetsTable renders Watch Targets as an aligned table with columns
// PID / WATCHING / PROCESS / ADDED / DIR / COMMAND.
//
// WATCHING and PROCESS are separate because they are separate facts: pcpm keeps
// a target's history after its process exits, and the user can stop watching
// something that is still running.
func WatchTargetsTable(statuses []watch.Status, now time.Time, home string, width int) string {
	rows := make([][]string, len(statuses))
	for i, s := range statuses {
		dir := ShortPath(s.Cwd, home, dirColumnWidth)
		if dir == "" {
			dir = "-"
		}
		rows[i] = []string{
			strconv.Itoa(int(s.PID)),
			yesNo(s.Watching()),
			runningOrGone(s.Running),
			Age(now, s.AddedAt),
			dir,
			s.Cmdline,
		}
	}
	return Grid([]string{"PID", "WATCHING", "PROCESS", "ADDED", "DIR", "COMMAND"}, rows, width)
}

// DaemonLine reports the collector's state as a single line beneath the target
// list. A stopped collector is the reason a target's history goes quiet, so it
// is worth saying plainly rather than leaving the user to infer it from a chart
// that stopped moving.
func DaemonLine(d watch.DaemonState, now time.Time) string {
	if !d.Running {
		return "\ncollector: not running — start it with `pcpm watch daemon`\n"
	}
	if d.LastTick.IsZero() {
		return fmt.Sprintf("\ncollector: running (pid %d), nothing collected yet\n", d.PID)
	}
	return fmt.Sprintf("\ncollector: running (pid %d), last collected %s ago\n",
		d.PID, Age(now, d.LastTick))
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func runningOrGone(b bool) string {
	if b {
		return "running"
	}
	return "gone"
}

// jsonWatchTarget is the machine-readable view of a Watch Target: every stored
// field untruncated, plus whether its process is still there. Times are
// RFC 3339; StoppedAt is null while pcpm is still watching.
type jsonWatchTarget struct {
	PID       int32   `json:"pid"`
	Name      string  `json:"name"`
	Cmdline   string  `json:"cmdline"`
	Cwd       string  `json:"cwd"`
	CreatedAt string  `json:"created_at"`
	AddedAt   string  `json:"added_at"`
	StoppedAt *string `json:"stopped_at"`
	Watching  bool    `json:"watching"`
	Running   bool    `json:"running"`
}

// WatchTargetJSON renders one Watch Target as a JSON object — the shape a
// command that acted on a single target should report.
func WatchTargetJSON(s watch.Status) (string, error) {
	return encodeJSON(watchTargetView(s))
}

// WatchTargetsJSON renders Watch Targets as an indented JSON array. No targets
// renders "[]" (never "null").
func WatchTargetsJSON(statuses []watch.Status) (string, error) {
	views := make([]jsonWatchTarget, len(statuses))
	for i, s := range statuses {
		views[i] = watchTargetView(s)
	}
	return encodeJSON(views)
}

// watchTargetView is the shared conversion behind the single- and multi-target
// JSON renderers.
func watchTargetView(s watch.Status) jsonWatchTarget {
	v := jsonWatchTarget{
		PID:      s.PID,
		Name:     s.Name,
		Cmdline:  s.Cmdline,
		Cwd:      s.Cwd,
		AddedAt:  s.AddedAt.UTC().Format(time.RFC3339),
		Watching: s.Watching(),
		Running:  s.Running,
	}
	if !s.Created.IsZero() {
		v.CreatedAt = s.Created.UTC().Format(time.RFC3339)
	}
	if s.StoppedAt != nil {
		stopped := s.StoppedAt.UTC().Format(time.RFC3339)
		v.StoppedAt = &stopped
	}
	return v
}

// ListenersTable renders listeners as an aligned table with columns
// PID / PORTS / AGE / NAME / COMMAND. PORTS is comma-joined; a network-exposed
// port (bound to all interfaces) gets a trailing "*".
func ListenersTable(ls []listen.Listener, now time.Time, width int) string {
	rows := make([][]string, len(ls))
	for i, l := range ls {
		rows[i] = []string{
			strconv.Itoa(int(l.PID)),
			formatPorts(l.Ports),
			Age(now, l.Created),
			l.Name,
			l.Cmdline,
		}
	}
	return Grid([]string{"PID", "PORTS", "AGE", "NAME", "COMMAND"}, rows, width)
}

// formatPorts joins a listener's ports as e.g. "3000,5000*", marking each
// network-exposed port with a trailing "*".
func formatPorts(ports []listen.Port) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		s := strconv.FormatUint(uint64(p.Number), 10)
		if p.Exposed {
			s += "*"
		}
		parts[i] = s
	}
	return strings.Join(parts, ",")
}

// fit shortens s to width, honouring the convention that a width of zero or
// less means "do not truncate" — which is what terminalWidth reports when
// output is piped or redirected. Passing such a width straight to truncate
// would blank the value instead.
func fit(s string, width int) string {
	if width <= 0 {
		return s
	}
	return truncate(s, width)
}

// fitCommand shortens a command line to width, keeping what identifies it.
//
// A command line carries its meaning at the two ends — the program at the
// front, what it was told to do at the back — with a long path in between.
// Cutting the tail throws the identifying half away: three targets running
// `bun /Users/…/gbrain serve` all shorten to the same prefix and stop being
// distinguishable at exactly the moment you are trying to tell them apart.
//
// So paths go first, collapsed to their last segment, which is where a path's
// own information is. That alone usually brings the line inside the width; when
// it does not, the middle is elided rather than the end.
func fitCommand(s string, width int) string {
	if width <= 0 || len([]rune(s)) <= width {
		return s
	}
	collapsed := collapsePaths(s)
	if len([]rune(collapsed)) <= width {
		return collapsed
	}
	// Below this there is not enough room for two meaningful ends, and an
	// elided middle degenerates into fragments like "……kdb". A prefix at least
	// names the program.
	const readableEnds = 16
	if width < readableEnds {
		return truncate(collapsed, width)
	}
	return elideMiddle(collapsed, width)
}

// collapsePaths replaces arguments that look like filesystem paths with their
// last segment. Two separators is the threshold: it catches `/usr/local/bin/x`
// and `~/a/b` while leaving `app.main:app` and `127.0.0.1:8570` alone.
func collapsePaths(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if strings.Count(f, "/") < 2 {
			continue
		}
		segments := strings.Split(strings.Trim(f, "/"), "/")
		fields[i] = ellipsis + "/" + segments[len(segments)-1]
	}
	return strings.Join(fields, " ")
}

// elideMiddle keeps both ends of s, dropping from the middle. The tail gets
// twice the room of the head: arguments say more about what a process is doing
// than the interpreter that launched it does.
func elideMiddle(s string, width int) string {
	runes := []rune(s)
	if width <= 1 {
		return ellipsis
	}
	keep := width - 1 // room for the ellipsis
	head := keep / 3
	tail := keep - head
	return string(runes[:head]) + ellipsis + string(runes[len(runes)-tail:])
}

// truncate shortens s to at most n bytes, marking any cut with a trailing
// ellipsis. It never cuts in the middle of a UTF-8 rune.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	// Reserve room for the ellipsis when it fits, then back the cut up to the
	// nearest rune boundary so we never emit an invalid UTF-8 fragment.
	cut := n
	if n > len(ellipsis) {
		cut = n - len(ellipsis)
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if n > len(ellipsis) {
		return s[:cut] + ellipsis
	}
	return s[:cut]
}
