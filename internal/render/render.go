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
	b, err := json.MarshalIndent(views, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

type cell struct{ pid, user, age, name, cmd string }

// Table renders candidates as an aligned text table with columns
// PID / USER / AGE / NAME / COMMAND. COMMAND is truncated so each row fits
// within width columns; width <= 0 disables truncation (e.g. when piped).
func Table(procs []orphan.Process, now time.Time, width int) string {
	header := cell{pid: "PID", user: "USER", age: "AGE", name: "NAME", cmd: "COMMAND"}

	rows := make([]cell, len(procs))
	wPID, wUser, wAge, wName := len(header.pid), len(header.user), len(header.age), len(header.name)
	for i, p := range procs {
		user := p.User
		if user == "" {
			user = strconv.Itoa(int(p.UID))
		}
		c := cell{
			pid:  strconv.Itoa(int(p.PID)),
			user: user,
			age:  Age(now, p.Created),
			name: p.Name,
			cmd:  p.Cmdline,
		}
		rows[i] = c
		wPID = max(wPID, len(c.pid))
		wUser = max(wUser, len(c.user))
		wAge = max(wAge, len(c.age))
		wName = max(wName, len(c.name))
	}

	var b strings.Builder
	writeRow := func(c cell) {
		prefix := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  ", wPID, c.pid, wUser, c.user, wAge, c.age, wName, c.name)
		cmd := c.cmd
		if width > 0 {
			cmd = truncate(cmd, max(0, width-len(prefix)))
		}
		b.WriteString(strings.TrimRight(prefix+cmd, " "))
		b.WriteByte('\n')
	}

	writeRow(header)
	for _, c := range rows {
		writeRow(c)
	}
	return b.String()
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
