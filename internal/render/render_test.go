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
	full := Grid(header, nil, rows, 0)
	lines := strings.Split(strings.TrimSuffix(full, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got %d lines:\n%s", len(lines), full)
	}
	col2 := strings.Index(lines[0], "NAME") // where the second column begins
	if !strings.HasPrefix(lines[1][col2:], "a ") || !strings.HasPrefix(lines[2][col2:], "longname") {
		t.Errorf("second column not aligned at offset %d:\n%s", col2, full)
	}

	// narrow width => last column shortened, no line exceeds the width
	//
	// Measured in runes, not bytes: a terminal column holds a character, so a
	// byte limit cuts a line carrying any non-ASCII short of the space it has.
	narrow := Grid(header, nil, rows, 30)
	for _, ln := range strings.Split(strings.TrimSuffix(narrow, "\n"), "\n") {
		if width := len([]rune(ln)); width > 30 {
			t.Errorf("line exceeds width 30 (%d runes): %q", width, ln)
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

// Piped output reports a width of 0, which by convention means "do not
// truncate". Passing it straight to truncate blanks the value instead — which
// is how the command line vanished from the summary's first line.
func TestWatchSummaryTextKeepsTheCommandWhenNotATerminal(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	s := watch.Status{
		Target:  watch.Target{PID: 100, Name: "bun", Cmdline: "bun run dev --port 3000", Cwd: "/proj", AddedAt: now.Add(-time.Hour)},
		Running: true,
	}
	sum := watch.Summary{
		Samples: 12, First: now.Add(-time.Minute), Last: now,
		CurrentCPUPercent: 20, PeakCPUPercent: 80,
		CurrentRSSBytes: 300 << 20, PeakRSSBytes: 400 << 20,
		Processes: []watch.ProcessUsage{{PID: 100, Name: "bun", CPUPercent: 20, RSSBytes: 300 << 20}},
	}

	out := WatchSummaryText(s, sum, time.Hour, now, "", 0)
	if !strings.Contains(out, "bun run dev --port 3000") {
		t.Errorf("the command line is missing from the report:\n%s", out)
	}
	for _, want := range []string{"watching", "running", "peak", "PID", "NAME", "CPU", "RSS"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

// Asking about a target the collector never reached should say so, rather than
// reporting a confident zero.
func TestWatchSummaryTextSaysWhenThereAreNoSamples(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	s := watch.Status{Target: watch.Target{PID: 100, Cmdline: "bun run dev", AddedAt: now}, Running: true}

	out := WatchSummaryText(s, watch.Summary{}, time.Hour, now, "", 0)
	if !strings.Contains(out, "no samples") {
		t.Errorf("want the report to say there is nothing to show:\n%s", out)
	}
	if !strings.Contains(out, "watch daemon") {
		t.Errorf("want a pointer at the likely cause (the collector is not running):\n%s", out)
	}
}

func TestBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.5 KB"},
		{300 << 20, "300 MB"},
		{3 << 30, "3.0 GB"},
	} {
		if got := Bytes(tc.in); got != tc.want {
			t.Errorf("Bytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A command line's information sits at its two ends — the program at the front,
// what it is doing at the back — with a long path in between. Cutting the tail
// throws away the identifying half: three `bun /Users/…/gbrain serve` targets
// all truncate to the same prefix and become indistinguishable.
func TestFitCommandKeepsWhatIdentifiesACommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		width int
		want  []string // fragments that must survive
	}{
		{
			"a path swallows the line",
			"bun /Users/quincy/.bun/bin/gbrain serve", 30,
			[]string{"bun", "gbrain", "serve"},
		},
		{
			"the program name is behind a long path",
			"/private/tmp/claude-501/-Users-quincy/scratchpad/inhomo-dr serve --addr 127.0.0.1:8570", 40,
			[]string{"inhomo-dr", "serve"},
		},
		{
			"no paths, so both ends are kept",
			"rtk proxy uv run uvicorn app.main:app --port 8766", 30,
			[]string{"rtk", "8766"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fitCommand(tc.in, tc.width)
			if len([]rune(got)) > tc.width {
				t.Errorf("result is %d runes wide, want at most %d: %q", len([]rune(got)), tc.width, got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("%q is missing %q", got, want)
				}
			}
		})
	}
}

func TestFitCommandLeavesShortValuesAlone(t *testing.T) {
	for _, s := range []string{"sleep 300", "280 MB", "", "70%"} {
		if got := fitCommand(s, 30); got != s {
			t.Errorf("fitCommand(%q) = %q, want it untouched", s, got)
		}
	}
}

// Collapsing paths is a display convenience. The machine-readable output must
// still carry what was actually running.
func TestJSONKeepsTheWholeCommandLine(t *testing.T) {
	long := "bun /Users/quincy/.bun/bin/gbrain serve --flag /a/very/long/path/here"
	out, err := WatchTargetsJSON([]watch.Status{
		{Target: watch.Target{PID: 1, Cmdline: long, AddedAt: time.Now()}},
	})
	if err != nil {
		t.Fatalf("WatchTargetsJSON: %v", err)
	}
	if !strings.Contains(out, long) {
		t.Errorf("the JSON output altered the command line:\n%s", out)
	}
}

// Terminal columns hold characters, not bytes. Limiting by bytes cuts a line
// A header is a fixed label. Eliding its middle turns COMMAND into "C…AND",
// which reads as neither the word nor an abbreviation of it.
func TestGridDoesNotElideTheMiddleOfAHeader(t *testing.T) {
	rows := [][]string{{"1", strings.Repeat("x", 200)}}

	out := Grid([]string{"PID", "COMMAND"}, nil, rows, 12)
	header := strings.Split(out, "\n")[0]

	if strings.Contains(header, "C…AND") || strings.Contains(header, "…AND") {
		t.Errorf("the header was elided in the middle: %q", header)
	}
}

// Two ends need room to be ends. Below that, a prefix says more than a pair of
// three-character fragments.
func TestFitCommandFallsBackToAPrefixWhenVeryNarrow(t *testing.T) {
	got := fitCommand("bun /Users/quincy/.bun/bin/gbrain serve", 8)

	if !strings.HasPrefix(got, "bun") {
		t.Errorf("fitCommand at width 8 = %q, want it to start with the program name", got)
	}
	if len([]rune(got)) > 8 {
		t.Errorf("result is %d runes wide, want at most 8: %q", len([]rune(got)), got)
	}
}

// A name in a padded column decides where every later column starts. Measuring
// it in bytes puts CJK four columns out for every word, which is how the DIR
// column ends up drifting right on rows the reader most wants to compare.
func TestGridAlignsWideCharactersWithTheRestOfTheColumn(t *testing.T) {
	rows := [][]string{
		{"100.0", "bash", "~/repo/pcpm"},
		{"12.8", "汽水音乐 Helper", "/"},
		{"4.8", "豆包浏览器", "~/y"},
		{"7.9", "Code Helper", "~/x"},
	}

	out := Grid([]string{"%CPU", "NAME", "DIR"}, nil, rows, 0)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	// Every line's last column must begin at the same screen offset.
	want := -1
	for i, ln := range lines {
		at := displayWidth(ln[:strings.LastIndex(ln, strings.Fields(ln)[len(strings.Fields(ln))-1])])
		if i == 0 {
			want = at
			continue
		}
		if at != want {
			t.Errorf("line %d starts its last column at column %d, want %d:\n%s", i, at, want, out)
		}
	}
}

func TestGridRightAlignsTheColumnsAskedFor(t *testing.T) {
	rows := [][]string{
		{"100.0", "9285", "bash"},
		{"12.8", "45570", "汽水音乐"},
		{"4.8", "2886", "claude"},
	}

	out := Grid([]string{"%CPU", "PID", "NAME"}, []Align{Right, Right, Left}, rows, 0)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	// Right-aligned means the figures end together: every %CPU cell finishes in
	// the same screen column, which is the whole reason to compare them.
	want := displayWidth("100.0")
	for i, ln := range lines[1:] {
		cell := headColumns(ln, want)
		if strings.HasSuffix(cell, " ") {
			t.Errorf("row %d: %%CPU cell %q is padded on the right, so it is not right-aligned:\n%s",
				i, cell, out)
		}
	}
}

// A row of CJK is twice as wide on screen as it has runes. Fitting it to the
// terminal by rune count emits a line that wraps.
func TestGridFitsTheLastColumnToDisplayColumns(t *testing.T) {
	rows := [][]string{{"1", strings.Repeat("目录", 40)}}

	out := Grid([]string{"PID", "COMMAND"}, nil, rows, 40)

	for _, ln := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if w := displayWidth(ln); w > 40 {
			t.Errorf("line is %d columns wide, want at most 40: %q", w, ln)
		}
	}
}

// A total shown without saying how much of its window it covers reads as
// complete. The counter behind traffic restarts with the collector, so a short
// window is ordinary rather than exceptional.
func TestTrafficLineDisclosesPartialCoverage(t *testing.T) {
	partial := TrafficLine(watch.Summary{
		Traffic: watch.Traffic{InBytes: 4 << 30, OutBytes: 380 << 20},
		Covered: 22 * time.Hour, TrafficCovered: 22 * time.Hour, Window: 24 * time.Hour,
	})
	if !strings.Contains(partial, "covering") || !strings.Contains(partial, "22h") {
		t.Errorf("a short window did not say so: %q", partial)
	}

	full := TrafficLine(watch.Summary{
		Traffic: watch.Traffic{InBytes: 4 << 30},
		Covered: 24 * time.Hour, TrafficCovered: 24 * time.Hour, Window: 24 * time.Hour,
	})
	if strings.Contains(full, "covering") {
		t.Errorf("a fully covered window should not caveat itself: %q", full)
	}
}

func TestTrafficSeriesTurnsBucketBytesIntoARate(t *testing.T) {
	at := TrafficSeries(10 * time.Second)
	value, _ := at(watch.Point{Traffic: watch.Traffic{InBytes: 600, OutBytes: 400}})

	if value != 100 {
		t.Errorf("1000 bytes over 10s = %v/s, want 100", value)
	}
	// A zero-width bucket must not divide by zero.
	if v, _ := TrafficSeries(0)(watch.Point{Traffic: watch.Traffic{InBytes: 5}}); v != 0 {
		t.Errorf("a zero bucket gave %v", v)
	}
}

// A source that failed and a process that sent nothing are the same zero in the
// database. Printing "0 B" for the first is a confident statement that nothing
// moved — the one outcome the design set out to avoid.
func TestTrafficLineSaysAbsenceRatherThanZero(t *testing.T) {
	line := TrafficLine(watch.Summary{
		Covered: time.Hour, TrafficCovered: 0, Window: time.Hour,
	})

	if strings.Contains(line, "0 B") {
		t.Errorf("an unmeasured window rendered as a figure: %q", line)
	}
	if !strings.Contains(line, "not measured") {
		t.Errorf("an unmeasured window should say so, got %q", line)
	}
}

// Traffic can stop being measured while CPU carries on, so its coverage is its
// own — reusing the samples' coverage would call a failed source fully covered.
func TestTrafficCoverageIsSeparateFromSampleCoverage(t *testing.T) {
	line := TrafficLine(watch.Summary{
		Traffic:        watch.Traffic{InBytes: 1 << 30},
		Covered:        24 * time.Hour, // CPU sampled throughout
		TrafficCovered: 6 * time.Hour,
		Window:         24 * time.Hour,
	})

	if !strings.Contains(line, "covering 6h") {
		t.Errorf("traffic coverage was not disclosed independently: %q", line)
	}
}
