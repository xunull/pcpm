package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/listen"
	"github.com/xunull/pcpm/internal/orphan"
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

func TestJSON(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// shell operators lock in both untruncated copy and no HTML-escaping
	longCmd := "sh -c 'a && b' " + strings.Repeat("x", 500)
	procs := []orphan.Process{
		{PID: 10, PPID: 1, UID: 501, User: "quincy", Name: "next-server", Cmdline: longCmd, Created: created},
	}

	out, err := JSON(procs)
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

	// every field present, IDs numeric, strings verbatim, cmdline UNtruncated
	for _, k := range []string{"pid", "ppid", "uid", "user", "name", "cmdline", "create_time"} {
		if _, ok := e[k]; !ok {
			t.Errorf("missing field %q in %v", k, e)
		}
	}
	if e["pid"] != float64(10) || e["ppid"] != float64(1) || e["uid"] != float64(501) {
		t.Errorf("numeric fields wrong: %v", e)
	}
	if e["user"] != "quincy" || e["name"] != "next-server" {
		t.Errorf("string fields wrong: %v", e)
	}
	if e["cmdline"] != longCmd {
		t.Errorf("cmdline was altered/truncated (len got %d, want %d)", len(e["cmdline"].(string)), len(longCmd))
	}
	if !strings.Contains(out, "&&") {
		t.Errorf("cmdline should not be HTML-escaped (& kept literal for jq); raw:\n%s", out)
	}
	if e["create_time"] != "2026-01-02T03:04:05Z" {
		t.Errorf("create_time = %v, want 2026-01-02T03:04:05Z", e["create_time"])
	}

	// no candidates -> "[]", not "null"
	empty, err := JSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(empty) != "[]" {
		t.Errorf("empty input: want [], got %q", empty)
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
