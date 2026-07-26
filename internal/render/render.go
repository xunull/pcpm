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
	writeRow := func(r []string) {
		var prefix strings.Builder
		for i := 0; i < cols-1; i++ {
			fmt.Fprintf(&prefix, "%-*s  ", w[i], at(r, i))
		}
		last := at(r, cols-1)
		if width > 0 {
			last = truncate(last, max(0, width-prefix.Len()))
		}
		b.WriteString(strings.TrimRight(prefix.String()+last, " "))
		b.WriteByte('\n')
	}

	writeRow(header)
	for _, r := range rows {
		writeRow(r)
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
