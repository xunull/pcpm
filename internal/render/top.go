package render

import (
	"fmt"
	"time"

	"github.com/xunull/pcpm/internal/top"
)

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
func TopTable(rows []top.Process, home string, width int) string {
	if len(rows) == 0 {
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
			formatPercent(p.CPUPercent),
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

// formatPercent keeps a figure readable across the range it actually spans: a
// process nudging a core reads 0.4, one saturating eight reads 800. Fixing the
// decimals at one would make the large numbers wider than they are informative.
func formatPercent(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
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

// TopJSON renders a ranking as an indented JSON array with nothing truncated.
// An empty ranking renders "[]" (never "null").
func TopJSON(rows []top.Process) (string, error) {
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
	return encodeJSON(views)
}
