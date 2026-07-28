package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/proc"
	"github.com/xunull/pcpm/internal/top"
)

func ranked() []top.Process {
	return []top.Process{
		{
			Process: proc.Process{
				PID: 4471, Name: "cc1plus", Cmdline: "/usr/bin/cc1plus -O2 " + strings.Repeat("x", 200),
				Cwd: "/Users/q/repo", Created: time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
			},
			CPUPercent: 812.4, RSSBytes: 512 << 20,
		},
		{
			Process:    proc.Process{PID: 45570, Name: "汽水音乐 Helper (Renderer)"},
			CPUPercent: 12.84, RSSBytes: 412 << 20,
		},
		{
			Process:    proc.Process{PID: 2886, Name: "claude"},
			CPUPercent: 0.0, RSSBytes: 600 << 20,
		},
	}
}

// A ranking exists to be compared down its columns, which only works when the
// digits line up.
func TestTopTableRightAlignsItsQuantities(t *testing.T) {
	out := TopTable(ranked(), 0)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	widest := displayWidth("812")
	for i, ln := range lines[1:] {
		cell := headColumns(ln, widest)
		if strings.HasSuffix(cell, " ") {
			t.Errorf("row %d has its %%CPU padded on the right (%q), so the figures do not line up:\n%s",
				i, cell, out)
		}
	}
}

// The percentage is the whole point of the ordering; it must not read as zero
// for a process using eight cores just because the format assumed one.
func TestTopTableShowsRatesAboveOneCore(t *testing.T) {
	out := TopTable(ranked(), 0)

	if !strings.Contains(out, "812") {
		t.Errorf("a rate above 100%% is missing from:\n%s", out)
	}
}

// A CJK process name must not push the columns after it out of true — the
// reason the table helper measures in terminal columns.
func TestTopTableKeepsItsColumnsWhenANameIsWide(t *testing.T) {
	out := TopTable(ranked(), 0)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	// NAME is the last column, so every line before it must be the same width.
	want := -1
	for i, ln := range lines {
		at := strings.LastIndex(ln, "  ") + 2
		got := displayWidth(ln[:at])
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Errorf("line %d starts NAME at column %d, want %d:\n%s", i, got, want, out)
		}
	}
}

func TestTopTableSaysWhenThereIsNothingToRank(t *testing.T) {
	if out := TopTable(nil, 0); !strings.Contains(out, "no processes") {
		t.Errorf("TopTable(nil) = %q", out)
	}
}

// The table has no room for a command line, so JSON is where it has to survive
// intact — that is the point of having both.
func TestTopJSONKeepsTheWholeCommandLine(t *testing.T) {
	body, err := TopJSON(ranked())
	if err != nil {
		t.Fatal(err)
	}
	var got []struct {
		PID        int32   `json:"pid"`
		Cmdline    string  `json:"cmdline"`
		CPUPercent float64 `json:"cpu_percent"`
		RSSBytes   int64   `json:"rss_bytes"`
		CreateTime string  `json:"create_time"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, body)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	if strings.Contains(got[0].Cmdline, ellipsis) || len(got[0].Cmdline) < 200 {
		t.Errorf("the command line was shortened: %q", got[0].Cmdline)
	}
	if got[0].CPUPercent != 812.4 {
		t.Errorf("cpu_percent = %v, want the unrounded 812.4", got[0].CPUPercent)
	}
	if got[0].CreateTime == "" {
		t.Error("create_time is missing")
	}
	if got[1].CreateTime != "" {
		t.Errorf("an unknown create time should be empty, got %q", got[1].CreateTime)
	}
}

func TestTopJSONRendersNothingAsAnEmptyArray(t *testing.T) {
	body, err := TopJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("TopJSON(nil) = %q, want []", body)
	}
}
