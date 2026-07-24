package render

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	longCmd := strings.Repeat("x", 500)
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
		t.Errorf("cmdline was altered/truncated (len got %d, want 500)", len(e["cmdline"].(string)))
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
