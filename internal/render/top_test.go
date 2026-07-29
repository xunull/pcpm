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
	out := TopTable(ranked(), top.Focus{}, "", 0)
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
	out := TopTable(ranked(), top.Focus{}, "", 0)

	if !strings.Contains(out, "812") {
		t.Errorf("a rate above 100%% is missing from:\n%s", out)
	}
}

// A CJK process name must not push the columns after it out of true — the
// reason the table helper measures in terminal columns.
func TestTopTableKeepsItsColumnsWhenANameIsWide(t *testing.T) {
	rows := []top.Process{
		{Process: proc.Process{PID: 1, Name: "bash", Cwd: "/one"}},
		{Process: proc.Process{PID: 2, Name: "汽水音乐 Helper", Cwd: "/two"}},
		{Process: proc.Process{PID: 3, Name: "豆包浏览器", Cwd: "/three"}},
		{Process: proc.Process{PID: 4, Name: "Code Helper", Cwd: "/four"}},
	}

	out := TopTable(rows, top.Focus{}, "", 0)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	// DIR is last, so where its cell begins is where every earlier column
	// finished. Byte- or rune-based padding puts these at different offsets.
	want := -1
	for i, ln := range lines {
		dir := []string{"DIR", "/one", "/two", "/three", "/four"}[i]
		got := displayWidth(ln[:strings.LastIndex(ln, dir)])
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Errorf("line %d starts DIR at column %d, want %d:\n%s", i, got, want, out)
		}
	}
}

// The marker is the reason to reach for pcpm rather than top; without a legend
// it is an unexplained punctuation mark.
func TestTopTableExplainsItsMarker(t *testing.T) {
	rows := []top.Process{
		{Process: proc.Process{PID: 1, Name: "bun"}, Forgotten: true},
		{Process: proc.Process{PID: 2, Name: "claude"}},
	}

	out := TopTable(rows, top.Focus{}, "", 0)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	if !strings.HasPrefix(lines[1], forgottenMark) {
		t.Errorf("the forgotten row is not marked: %q", lines[1])
	}
	if strings.HasPrefix(lines[2], forgottenMark) {
		t.Errorf("an ordinary row was marked: %q", lines[2])
	}
	if !strings.Contains(out, "pcpm forgotten") {
		t.Errorf("the marker is not explained:\n%s", out)
	}
}

// Columns that say nothing cost width the remaining ones could use. APP is
// empty for everything outside a macOS bundle, which on Linux is everything.
func TestTopTableDropsColumnsWithNothingInThem(t *testing.T) {
	plain := TopTable([]top.Process{
		{Process: proc.Process{PID: 1, Name: "bun", Exe: "/opt/homebrew/bin/bun", Cwd: "/x"}},
	}, top.Focus{}, "", 0)

	if strings.Contains(plain, "APP") {
		t.Errorf("an APP column was rendered with nothing in it:\n%s", plain)
	}
	if strings.Contains(plain, "pcpm forgotten") {
		t.Errorf("the marker legend appeared with nothing marked:\n%s", plain)
	}

	bundled := TopTable([]top.Process{
		{Process: proc.Process{PID: 1, Name: "stable", Exe: "/Applications/Warp.app/Contents/MacOS/stable"}},
	}, top.Focus{}, "", 0)

	if !strings.Contains(bundled, "APP") || !strings.Contains(bundled, "Warp") {
		t.Errorf("the APP column is missing when a process has one:\n%s", bundled)
	}
}

func TestTopTableSaysWhenThereIsNothingToRank(t *testing.T) {
	if out := TopTable(nil, top.Focus{}, "", 0); !strings.Contains(out, "no processes") {
		t.Errorf("TopTable(nil) = %q", out)
	}
}

// The table has no room for a command line, so JSON is where it has to survive
// intact — that is the point of having both.
func TestTopJSONKeepsTheWholeCommandLine(t *testing.T) {
	body, err := TopJSON(ranked(), top.Totals{Cores: 10, BusyPercent: 900}.WithRanked(top.Sum{CPUPercent: 825}))
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		CPU struct {
			Cores               int     `json:"cores"`
			BusyPercent         float64 `json:"busy_percent"`
			UnattributedPercent float64 `json:"unattributed_percent"`
		} `json:"cpu"`
		Processes []struct {
			PID        int32   `json:"pid"`
			Cmdline    string  `json:"cmdline"`
			CPUPercent float64 `json:"cpu_percent"`
			RSSBytes   int64   `json:"rss_bytes"`
			CreateTime string  `json:"create_time"`
		} `json:"processes"`
	}
	if err := json.Unmarshal([]byte(body), &frame); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, body)
	}
	got := frame.Processes
	if frame.CPU.Cores != 10 || frame.CPU.BusyPercent != 900 {
		t.Errorf("the machine figures are missing from the JSON: %+v", frame.CPU)
	}
	if frame.CPU.UnattributedPercent != 75 {
		t.Errorf("unattributed = %v, want 75", frame.CPU.UnattributedPercent)
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
	body, err := TopJSON(nil, top.Totals{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"processes": []`) {
		t.Errorf("TopJSON with no processes should carry an empty array, got:\n%s", body)
	}
}

// A reader has to be able to tell whether the rows cover the machine or two
// thirds of it, and what to do about the difference.
func TestTopHeaderStatesTheGapAndItsRemedy(t *testing.T) {
	out := TopHeader(top.Totals{
		Cores: 10, BusyPercent: 699,
		MemoryUsedBytes: 52 << 30, MemoryTotalBytes: 64 << 30,
	}.WithRanked(top.Sum{CPUPercent: 551}))

	for _, want := range []string{"699%", "1000%", "10 cores", "551%", "148%", "sudo"} {
		if !strings.Contains(out, want) {
			t.Errorf("header is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "52 GB") || !strings.Contains(out, "64 GB") {
		t.Errorf("header is missing the memory figures:\n%s", out)
	}
}

// Running as root removes the remedy but not the gap: kernel_task is PID 0 and
// cannot be read at any privilege, so a residual remains and hiding it would be
// assuming it away.
func TestACompleteRankingStillReportsItsResidual(t *testing.T) {
	out := TopHeader(top.Totals{Cores: 10, BusyPercent: 699, Complete: true}.WithRanked(top.Sum{CPUPercent: 690}))

	if !strings.Contains(out, "unattributed 9.0%") {
		t.Errorf("a complete ranking dropped its residual:\n%s", out)
	}
	if strings.Contains(out, "sudo") {
		t.Errorf("a complete ranking has no remedy left to suggest:\n%s", out)
	}
}

// A row kept by a word buried in the middle of its path looked arbitrary: the
// column always collapsed to the tail, so the reason was never on screen.
func TestTopTableCollapsesTheDirectoryAroundWhatKeptTheRow(t *testing.T) {
	rows := []top.Process{{Process: proc.Process{
		PID: 1, Name: "node", Cwd: "/Users/q/xunull-repository/xunull-github/open-source/pcpm",
	}}}

	plain := TopTable(rows, top.Focus{}, "/Users/q", 200)
	if strings.Contains(plain, "xunull-repository") {
		t.Fatalf("this path no longer needs collapsing; the test proves nothing:\n%s", plain)
	}

	focused := TopTable(rows, top.ParseFocus("dir:xunull-repository"), "/Users/q", 200)
	if !strings.Contains(focused, "xunull-repository") {
		t.Errorf("the column does not show why the row was kept:\n%s", focused)
	}
}

// A row kept for its name says nothing about its directory, so re-centring the
// column would be answering a question nobody asked.
func TestTopTableLeavesTheDirectoryAloneForANonDirectoryMatch(t *testing.T) {
	rows := []top.Process{{Process: proc.Process{
		PID: 1, Name: "node", Cwd: "/Users/q/xunull-repository/xunull-github/open-source/pcpm",
	}}}

	plain := TopTable(rows, top.Focus{}, "/Users/q", 200)
	byName := TopTable(rows, top.ParseFocus("name:node"), "/Users/q", 200)

	if plain != byName {
		t.Errorf("the directory column changed for a name match:\n plain: %s\n focus: %s", plain, byName)
	}
}
