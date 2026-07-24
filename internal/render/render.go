// Package render turns orphan candidates into human- and machine-readable
// output for the pcpm CLI.
package render

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xunull/pcpm/internal/listen"
	"github.com/xunull/pcpm/internal/orphan"
)

// ellipsis marks a value that has been cut short to fit its column.
const ellipsis = "…"

// Format selects how the orphans command renders its candidates.
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

// jsonProc is the machine-readable view of a candidate: every field, none
// truncated. CreateTime is RFC 3339, or empty when the process start time is
// unknown.
type jsonProc struct {
	PID        int32  `json:"pid"`
	PPID       int32  `json:"ppid"`
	UID        int32  `json:"uid"`
	User       string `json:"user"`
	Name       string `json:"name"`
	Cmdline    string `json:"cmdline"`
	CreateTime string `json:"create_time"`
}

// JSON renders candidates as an indented JSON array with all fields untruncated,
// suitable for jq or scripting. No candidates renders "[]" (never "null").
func JSON(procs []orphan.Process) (string, error) {
	views := make([]jsonProc, len(procs))
	for i, p := range procs {
		v := jsonProc{
			PID:     p.PID,
			PPID:    p.PPID,
			UID:     p.UID,
			User:    p.User,
			Name:    p.Name,
			Cmdline: p.Cmdline,
		}
		if !p.Created.IsZero() {
			v.CreateTime = p.Created.UTC().Format(time.RFC3339)
		}
		views[i] = v
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	// cmdlines routinely contain &, <, >; keep them literal for jq and humans.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(views); err != nil {
		return "", err
	}
	return b.String(), nil
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
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(views); err != nil {
		return "", err
	}
	return b.String(), nil
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
		for i := 0; i < cols && i < len(r); i++ {
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

// Table renders orphan candidates as an aligned table with columns
// PID / USER / AGE / NAME / COMMAND, in the order the caller supplies.
func Table(procs []orphan.Process, now time.Time, width int) string {
	rows := make([][]string, len(procs))
	for i, p := range procs {
		user := p.User
		if user == "" {
			user = strconv.Itoa(int(p.UID))
		}
		rows[i] = []string{
			strconv.Itoa(int(p.PID)),
			user,
			Age(now, p.Created),
			p.Name,
			p.Cmdline,
		}
	}
	return Grid([]string{"PID", "USER", "AGE", "NAME", "COMMAND"}, rows, width)
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
