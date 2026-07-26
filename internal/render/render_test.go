package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/forgotten"
	"github.com/xunull/pcpm/internal/listen"
	"github.com/xunull/pcpm/internal/proc"
	"github.com/xunull/pcpm/internal/watch"
)

func TestAge(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		created time.Time
		want    string
	}{
		{"days", now.Add(-(3*24*time.Hour + 4*time.Hour + 30*time.Minute)), "3d4h"},
		{"hours", now.Add(-(6*time.Hour + 12*time.Minute)), "6h12m"},
		{"minutes", now.Add(-45 * time.Minute), "45m"},
		{"seconds", now.Add(-30 * time.Second), "30s"},
		{"future clamps to zero", now.Add(5 * time.Minute), "0s"},
		{"unknown created time", time.Time{}, "?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Age(now, tc.created); got != tc.want {
				t.Errorf("Age(now, created) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShortPath(t *testing.T) {
	const home = "/Users/me"
	cases := []struct {
		name   string
		path   string
		maxLen int
		want   string
	}{
		{"home becomes tilde", "/Users/me/proj", 40, "~/proj"},
		{"non-home path kept", "/opt/homebrew/var", 40, "/opt/homebrew/var"},
		{"empty path", "", 40, ""},
		{"long path keeps last two segments", "/Users/me/a/b/c/open-source/ocrserver", 20, "…/open-source/ocrserver"},
		{"short enough is untouched", "/Users/me/a", 40, "~/a"},
		{"home itself", "/Users/me", 40, "~"},
		{"another user is not a home prefix", "/Users/melissa/proj", 40, "/Users/melissa/proj"},
		{"no home known", "/Users/me/proj", 40, "~/proj"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShortPath(tc.path, home, tc.maxLen); got != tc.want {
				t.Errorf("ShortPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestGrid(t *testing.T) {
	header := []string{"PID", "NAME", "CMD"}
	rows := [][]string{
		{"1", "a", "short"},
		{"1000", "longname", strings.Repeat("x", 40)},
	}

	// width 0 => no truncation; header + 2 rows, columns aligned
	full := Grid(header, rows, 0)
	lines := strings.Split(strings.TrimSuffix(full, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got %d lines:\n%s", len(lines), full)
	}
	col2 := strings.Index(lines[0], "NAME") // where the second column begins
	if !strings.HasPrefix(lines[1][col2:], "a ") || !strings.HasPrefix(lines[2][col2:], "longname") {
		t.Errorf("second column not aligned at offset %d:\n%s", col2, full)
	}

	// narrow width => last column truncated with an ellipsis, no line exceeds width
	narrow := Grid(header, rows, 30)
	for _, ln := range strings.Split(strings.TrimSuffix(narrow, "\n"), "\n") {
		if len(ln) > 30 {
			t.Errorf("line exceeds width 30 (%d bytes): %q", len(ln), ln)
		}
	}
	if !strings.Contains(narrow, "…") {
		t.Errorf("expected an ellipsis in truncated output:\n%s", narrow)
	}
}

// The table must carry PGID: it is the value `kill -- -<PGID>` needs, and it
// differs from the root's PID — reaching for PID instead is the natural mistake.
func TestForgottenTableShowsProcessGroup(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	trees := []forgotten.Tree{{
		Root: proc.Process{
			PID: 58714, PGID: 58669,
			Cmdline: "uv run uvicorn", Cwd: "/Users/me/proj",
			Created: now.Add(-2 * time.Hour),
		},
		Procs: 3,
		Ports: []listen.Port{{Number: 8766}},
	}}

	out := ForgottenTable(trees, now, "/Users/me", 0)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want header + 1 row, got %d lines:\n%s", len(lines), out)
	}
	header, row := lines[0], lines[1]

	if !strings.Contains(header, "PGID") {
		t.Errorf("header is missing the PGID column:\n%s", header)
	}
	if !strings.Contains(row, "58669") {
		t.Errorf("row does not show the process group 58669:\n%s", row)
	}
	// PGID sits next to PID, before the rest
	iPID, iPGID, iAge := strings.Index(header, "PID"), strings.Index(header, "PGID"), strings.Index(header, "AGE")
	if !(iPID < iPGID && iPGID < iAge) {
		t.Errorf("want column order PID, PGID, AGE; got header:\n%s", header)
	}
	// the other columns survive
	for _, want := range []string{"8766", "3", "~/proj", "uv run uvicorn"} {
		if !strings.Contains(row, want) {
			t.Errorf("row is missing %q:\n%s", want, row)
		}
	}
}

func TestForgottenJSON(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	longCmd := "sh -c 'a && b' " + strings.Repeat("x", 500)
	trees := []forgotten.Tree{{
		Root: proc.Process{
			PID: 100, PPID: 1, PGID: 900, UID: 501, User: "quincy",
			Name: "uv", Cmdline: longCmd, Cwd: "/proj", Created: created,
		},
		Procs: 3,
		Ports: []listen.Port{{Number: 8766, Exposed: false}, {Number: 9000, Exposed: true}},
	}}

	out, err := ForgottenJSON(trees)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 element, got %d", len(got))
	}
	e := got[0]
	for _, k := range []string{"pid", "ppid", "pgid", "uid", "user", "name", "cmdline", "cwd", "create_time", "procs", "ports"} {
		if _, ok := e[k]; !ok {
			t.Errorf("missing field %q in %v", k, e)
		}
	}
	if e["cmdline"] != longCmd {
		t.Errorf("cmdline was altered/truncated")
	}
	if !strings.Contains(out, "&&") {
		t.Errorf("cmdline should not be HTML-escaped; raw:\n%s", out)
	}
	if e["cwd"] != "/proj" || e["procs"] != float64(3) {
		t.Errorf("cwd/procs wrong: %v", e)
	}
	ports, ok := e["ports"].([]any)
	if !ok || len(ports) != 2 {
		t.Fatalf("ports: want a 2-element array, got %v", e["ports"])
	}
	p1 := ports[1].(map[string]any)
	if p1["port"] != float64(9000) || p1["exposed"] != true {
		t.Errorf("port[1] = %v, want {port:9000, exposed:true}", p1)
	}

	empty, err := ForgottenJSON(nil)
	if err != nil || strings.TrimSpace(empty) != "[]" {
		t.Errorf("empty input: want [] and no error, got %q err=%v", empty, err)
	}
}

func TestListenersJSON(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ls := []listen.Listener{{
		PID: 10, UID: 501, User: "quincy", Name: "node", Cmdline: "node server.js", Created: created,
		Ports: []listen.Port{{Number: 3000, Exposed: false}, {Number: 5000, Exposed: true}},
	}}

	out, err := ListenersJSON(ls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 element, got %d", len(got))
	}
	e := got[0]
	for _, k := range []string{"pid", "uid", "user", "name", "cmdline", "create_time", "ports"} {
		if _, ok := e[k]; !ok {
			t.Errorf("missing field %q in %v", k, e)
		}
	}
	ports, ok := e["ports"].([]any)
	if !ok || len(ports) != 2 {
		t.Fatalf("ports: want a 2-element array, got %v", e["ports"])
	}
	p0 := ports[0].(map[string]any)
	p1 := ports[1].(map[string]any)
	if p0["port"] != float64(3000) || p0["exposed"] != false {
		t.Errorf("port[0] = %v, want {port:3000, exposed:false}", p0)
	}
	if p1["port"] != float64(5000) || p1["exposed"] != true {
		t.Errorf("port[1] = %v, want {port:5000, exposed:true}", p1)
	}

	empty, err := ListenersJSON(nil)
	if err != nil || strings.TrimSpace(empty) != "[]" {
		t.Errorf("empty input: want [] and no error, got %q err=%v", empty, err)
	}
}

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"table", ""} {
		if f, err := ParseFormat(s); err != nil || f != FormatTable {
			t.Errorf("ParseFormat(%q) = %v, %v; want FormatTable, nil", s, f, err)
		}
	}
	if f, err := ParseFormat("json"); err != nil || f != FormatJSON {
		t.Errorf("ParseFormat(\"json\") = %v, %v; want FormatJSON, nil", f, err)
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("ParseFormat(\"yaml\"): want error, got nil")
	}
}

func TestWatchTargetsTableSeparatesWatchingFromRunning(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	stopped := now.Add(-time.Hour)
	statuses := []watch.Status{
		// still watched, process alive
		{Target: watch.Target{PID: 100, Name: "bun", Cmdline: "bun run dev", Cwd: "/proj", AddedAt: now.Add(-2 * time.Hour)}, Running: true},
		// still watched, but the process died — the row must stay
		{Target: watch.Target{PID: 200, Name: "uv", Cmdline: "uv run app", Cwd: "/other", AddedAt: now.Add(-3 * time.Hour)}, Running: false},
		// user stopped watching something that is still running
		{Target: watch.Target{PID: 300, Name: "node", Cmdline: "node s.js", Cwd: "/x", AddedAt: now.Add(-4 * time.Hour), StoppedAt: &stopped}, Running: true},
	}

	out := WatchTargetsTable(statuses, now, "", 200)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	if len(lines) != 4 {
		t.Fatalf("want a header plus 3 rows, got %d lines:\n%s", len(lines), out)
	}
	for _, col := range []string{"PID", "WATCHING", "PROCESS", "ADDED", "DIR", "COMMAND"} {
		if !strings.Contains(lines[0], col) {
			t.Errorf("header is missing %q:\n%s", col, lines[0])
		}
	}
	// The two facts are independent: every combination must be representable.
	for i, want := range [][2]string{{"yes", "running"}, {"yes", "gone"}, {"no", "running"}} {
		fields := strings.Fields(lines[i+1])
		if fields[1] != want[0] || fields[2] != want[1] {
			t.Errorf("row %d: watching/process = %s/%s, want %s/%s", i+1, fields[1], fields[2], want[0], want[1])
		}
	}
}

func TestWatchTargetsJSON(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	stopped := now.Add(-time.Hour)

	empty, err := WatchTargetsJSON(nil)
	if err != nil {
		t.Fatalf("WatchTargetsJSON(nil): %v", err)
	}
	if strings.TrimSpace(empty) != "[]" {
		t.Errorf("no targets should render [] not null, got %q", strings.TrimSpace(empty))
	}

	out, err := WatchTargetsJSON([]watch.Status{
		{Target: watch.Target{PID: 100, Name: "bun", Created: now.Add(-9 * time.Hour), AddedAt: now.Add(-2 * time.Hour)}, Running: true},
		{Target: watch.Target{PID: 200, Name: "uv", Created: now.Add(-9 * time.Hour), AddedAt: now.Add(-3 * time.Hour), StoppedAt: &stopped}},
	})
	if err != nil {
		t.Fatalf("WatchTargetsJSON: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got[0]["watching"] != true || got[0]["running"] != true {
		t.Errorf("first target: want watching+running true, got %v", got[0])
	}
	if got[0]["stopped_at"] != nil {
		t.Errorf("a watched target should have stopped_at null, got %v", got[0]["stopped_at"])
	}
	if got[1]["watching"] != false || got[1]["stopped_at"] == nil {
		t.Errorf("second target: want watching false with a stopped_at, got %v", got[1])
	}
	// create_time is the disambiguator for PID reuse, so it must be exported
	if got[0]["created_at"] == "" || got[0]["created_at"] == nil {
		t.Error("created_at is missing; it is what distinguishes a reused PID")
	}
}
