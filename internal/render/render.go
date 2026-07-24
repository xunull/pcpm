// Package render turns orphan candidates into human- and machine-readable
// output for the pcpm CLI.
package render

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xunull/pcpm/internal/orphan"
)

// Age renders how long a process has been running as a compact two-unit string
// like "3d4h", "6h12m", "45m", or "30s". A created time in the future (clock
// skew) clamps to "0s".
func Age(now, created time.Time) string {
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

// Table renders candidates as an aligned text table with columns
// PID / USER / AGE / NAME / COMMAND. COMMAND is truncated so each row fits
// within width columns; width <= 0 disables truncation (e.g. when piped).
func Table(procs []orphan.Process, now time.Time, width int) string {
	const (
		hPID  = "PID"
		hUser = "USER"
		hAge  = "AGE"
		hName = "NAME"
		hCmd  = "COMMAND"
	)
	type cell struct{ pid, user, age, name, cmd string }
	rows := make([]cell, len(procs))
	wPID, wUser, wAge, wName := len(hPID), len(hUser), len(hAge), len(hName)
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
	writeRow := func(pid, user, age, name, cmd string) {
		prefix := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  ", wPID, pid, wUser, user, wAge, age, wName, name)
		if width > 0 {
			if avail := width - len(prefix); avail >= 0 {
				cmd = truncate(cmd, avail)
			}
		}
		b.WriteString(strings.TrimRight(prefix+cmd, " "))
		b.WriteByte('\n')
	}

	writeRow(hPID, hUser, hAge, hName, hCmd)
	for _, c := range rows {
		writeRow(c.pid, c.user, c.age, c.name, c.cmd)
	}
	return b.String()
}

// truncate shortens s to at most n bytes, marking any cut with a trailing "…".
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
