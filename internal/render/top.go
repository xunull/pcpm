package render

import (
	"fmt"
	"time"

	"github.com/xunull/pcpm/internal/top"
)

// TopTable renders a CPU ranking as an aligned table.
//
// The quantities are right-aligned because comparing them is the only reason
// the table is ordered; ragged digits make the eye do arithmetic the layout
// should have done.
func TopTable(rows []top.Process, width int) string {
	if len(rows) == 0 {
		return "no processes to rank\n"
	}
	body := make([][]string, len(rows))
	for i, p := range rows {
		body[i] = []string{
			formatPercent(p.CPUPercent),
			Bytes(p.RSSBytes),
			fmt.Sprint(p.PID),
			p.Name,
		}
	}
	return Grid(
		[]string{"%CPU", "RSS", "PID", "NAME"},
		[]Align{Right, Right, Right, Left},
		body, width,
	)
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
	PID        int32   `json:"pid"`
	PPID       int32   `json:"ppid"`
	UID        int32   `json:"uid"`
	User       string  `json:"user"`
	Name       string  `json:"name"`
	Cmdline    string  `json:"cmdline"`
	Cwd        string  `json:"cwd"`
	CreateTime string  `json:"create_time"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSBytes   int64   `json:"rss_bytes"`
}

// TopJSON renders a ranking as an indented JSON array with nothing truncated.
// An empty ranking renders "[]" (never "null").
func TopJSON(rows []top.Process) (string, error) {
	views := make([]jsonTop, len(rows))
	for i, p := range rows {
		v := jsonTop{
			PID:        p.PID,
			PPID:       p.PPID,
			UID:        p.UID,
			User:       p.User,
			Name:       p.Name,
			Cmdline:    p.Cmdline,
			Cwd:        p.Cwd,
			CPUPercent: p.CPUPercent,
			RSSBytes:   p.RSSBytes,
		}
		if !p.Created.IsZero() {
			v.CreateTime = p.Created.UTC().Format(time.RFC3339)
		}
		views[i] = v
	}
	return encodeJSON(views)
}
