package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/xunull/pcpm/internal/top"
)

// TopHeader states what the machine was doing over the same window the rows
// cover, and how much of it the rows account for.
//
// Without it a reader has no way to tell whether the ranking covers everything
// or two thirds of it. The machine's own totals need no privilege, so the gap
// can be given as a quantity instead of left as an absence — and named, so that
// a reader who sees it grow knows what to do about it (ADR-0011).
func TopHeader(t top.Totals) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CPU  %s%% of %s%% (%d cores)  ·  attributed %s%%  ·  unattributed %s%%",
		rankedPercent(t.BusyPercent), rankedPercent(t.Capacity()), t.Cores,
		rankedPercent(t.AttributedPercent), rankedPercent(t.UnattributedPercent()))
	// The gap is always shown. Hiding it when running as root would assume it
	// falls to zero there, and it does not: kernel_task is PID 0, which cannot
	// be read at any privilege. Only the remedy is conditional, because there
	// is no remedy left once the ranking already covers every process.
	if !t.Complete {
		b.WriteString(" (needs sudo)")
	}
	b.WriteByte('\n')
	if t.MemoryTotalBytes > 0 {
		fmt.Fprintf(&b, "MEM  %s / %s\n", Bytes(t.MemoryUsedBytes), Bytes(t.MemoryTotalBytes))
	}
	return b.String()
}

// forgottenMark flags a row whose process belongs to a tree nothing is looking
// after any more.
const forgottenMark = "!"

// TopTable renders a CPU ranking as an aligned table.
//
// The quantities are right-aligned because comparing them is the only reason
// the table is ordered; ragged digits make the eye do arithmetic the layout
// should have done.
//
// Two columns appear only when they have something to say. APP is empty for
// every process outside a macOS bundle, which on Linux is all of them; the
// marker column is empty when nothing is forgotten. Rendering either as a strip
// of blanks would cost width that DIR can use.
func TopTable(rows []top.Process, focus top.Focus, home string, width int) string {
	if len(rows) == 0 {
		// The two empty tables mean opposite things. "No processes to rank"
		// under a focus would say the machine is idle, when what happened is
		// that the reader asked for something the machine is not running.
		if focus.Active() {
			return fmt.Sprintf("nothing matches %s — press / to change it\n", focus)
		}
		return "no processes to rank\n"
	}

	var anyApp, anyMark bool
	for _, p := range rows {
		anyApp = anyApp || p.Application() != ""
		anyMark = anyMark || p.Forgotten
	}

	header := []string{"%CPU", "RSS", "PID", "NAME"}
	align := []Align{Right, Right, Right, Left}
	if anyMark {
		header = append([]string{" "}, header...)
		align = append([]Align{Left}, align...)
	}
	if anyApp {
		header = append(header, "APP")
		align = append(align, Left)
	}
	// DIR goes last because it is the one column that shortens without becoming
	// wrong: a path collapses to its final segments and still names a place,
	// where half an application's name names nothing.
	header = append(header, "DIR")
	align = append(align, Left)

	body := make([][]string, len(rows))
	for i, p := range rows {
		cells := []string{
			rankedPercent(p.CPUPercent),
			Bytes(p.RSSBytes),
			fmt.Sprint(p.PID),
			p.Name,
		}
		if anyMark {
			mark := " "
			if p.Forgotten {
				mark = forgottenMark
			}
			cells = append([]string{mark}, cells...)
		}
		if anyApp {
			cells = append(cells, p.Application())
		}
		cells = append(cells, ShortPath(p.Cwd, home, dirColumnWidth))
		body[i] = cells
	}

	out := Grid(header, align, body, width)
	if anyMark {
		out += "\n" + forgottenMark + " nothing is looking after this — see `pcpm forgotten`\n"
	}
	return out
}

// jsonTop is the machine-readable view of a ranked process: the figures, plus
// every identifying field including the command line the table has no room for.
type jsonTop struct {
	PID         int32   `json:"pid"`
	PPID        int32   `json:"ppid"`
	UID         int32   `json:"uid"`
	User        string  `json:"user"`
	Name        string  `json:"name"`
	Cmdline     string  `json:"cmdline"`
	Exe         string  `json:"exe"`
	Cwd         string  `json:"cwd"`
	Application string  `json:"application"`
	CreateTime  string  `json:"create_time"`
	CPUPercent  float64 `json:"cpu_percent"`
	RSSBytes    int64   `json:"rss_bytes"`
	Forgotten   bool    `json:"forgotten"`
}

// jsonTopFrame is the machine-readable view of a whole ranking. Unlike the
// other commands this is an object rather than an array: the machine's own
// figures belong with the rows they are to be compared against, and the table
// puts them in a header for the same reason.
type jsonTopFrame struct {
	CPU       jsonTopCPU    `json:"cpu"`
	Memory    jsonTopMemory `json:"memory"`
	Processes []jsonTop     `json:"processes"`
}

type jsonTopCPU struct {
	Cores             int     `json:"cores"`
	BusyPercent       float64 `json:"busy_percent"`
	CapacityPercent   float64 `json:"capacity_percent"`
	AttributedPercent float64 `json:"attributed_percent"`
	// Unattributed is busy CPU no listed process accounts for. It is zero when
	// complete is true.
	UnattributedPercent float64 `json:"unattributed_percent"`
	Complete            bool    `json:"complete"`
}

type jsonTopMemory struct {
	UsedBytes  int64 `json:"used_bytes"`
	TotalBytes int64 `json:"total_bytes"`
}

// TopJSON renders a ranking, and the machine figures beside it, as an indented
// JSON object with nothing truncated. No processes renders "[]" (never "null").
func TopJSON(rows []top.Process, t top.Totals) (string, error) {
	frame := jsonTopFrame{
		CPU: jsonTopCPU{
			Cores:               t.Cores,
			BusyPercent:         t.BusyPercent,
			CapacityPercent:     t.Capacity(),
			AttributedPercent:   t.AttributedPercent,
			UnattributedPercent: t.UnattributedPercent(),
			Complete:            t.Complete,
		},
		Memory: jsonTopMemory{UsedBytes: t.MemoryUsedBytes, TotalBytes: t.MemoryTotalBytes},
	}
	views := make([]jsonTop, len(rows))
	for i, p := range rows {
		v := jsonTop{
			PID:         p.PID,
			PPID:        p.PPID,
			UID:         p.UID,
			User:        p.User,
			Name:        p.Name,
			Cmdline:     p.Cmdline,
			Exe:         p.Exe,
			Cwd:         p.Cwd,
			Application: p.Application(),
			CPUPercent:  p.CPUPercent,
			RSSBytes:    p.RSSBytes,
			Forgotten:   p.Forgotten,
		}
		if !p.Created.IsZero() {
			v.CreateTime = p.Created.UTC().Format(time.RFC3339)
		}
		views[i] = v
	}
	frame.Processes = views
	return encodeJSON(frame)
}
