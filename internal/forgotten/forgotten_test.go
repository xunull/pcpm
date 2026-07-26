package forgotten

import (
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/listen"
	"github.com/xunull/pcpm/internal/proc"
)

// procs models a machine with two forgotten trees plus the things that must NOT
// be flagged: a self-leading daemon, a helper whose parent shares its dead
// group, a system daemon, a GUI app helper, and a shell.
func procs(base time.Time) []proc.Process {
	return []proc.Process{
		{PID: 1, PPID: 0, PGID: 1, Cmdline: "/sbin/launchd"},

		// forgotten root: group 900's leader is gone, parent (launchd) is in group 1
		{PID: 100, PPID: 1, PGID: 900, Name: "uv", Cmdline: "uv run uvicorn app:app", Cwd: "/proj", Created: base},
		// its descendants: parent alive and in the same group -> not roots themselves
		{PID: 101, PPID: 100, PGID: 900, Name: "python", Cmdline: "python -m uvicorn", Created: base},
		{PID: 102, PPID: 101, PGID: 900, Name: "python", Cmdline: "python worker", Created: base},

		// proper daemon: setsid'd, so it leads its own (living) group
		{PID: 200, PPID: 1, PGID: 200, Name: "redis-server", Cmdline: "/opt/homebrew/opt/redis/bin/redis-server", Created: base.Add(-time.Hour)},

		// shell in a dead group (noise), and a helper whose parent shares that group
		{PID: 300, PPID: 1, PGID: 800, Name: "zsh", Cmdline: "-zsh"},
		{PID: 301, PPID: 300, PGID: 800, Name: "gitstatusd", Cmdline: "/Users/me/.cache/gitstatus/gitstatusd"},

		// noise: system-path daemon and GUI app helper, both in dead groups
		{PID: 400, PPID: 1, PGID: 901, Name: "somed", Cmdline: "/usr/libexec/somed"},
		{PID: 500, PPID: 1, PGID: 902, Name: "Helper", Cmdline: "/Applications/Google Chrome.app/Contents/Frameworks/Helper"},

		// a second, newer forgotten root with no descendants
		{PID: 600, PPID: 1, PGID: 903, Name: "bun", Cmdline: "bun serve", Cwd: "/game", Created: base.Add(2 * time.Hour)},
	}
}

func TestDetect(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ports := map[int32][]listen.Port{
		101: {{Number: 8766, Exposed: false}}, // a descendant holds the port
		200: {{Number: 6379, Exposed: false}}, // the daemon's port must not show up
	}

	got := Detect(procs(base), ports)

	if len(got) != 2 {
		var pids []int32
		for _, tr := range got {
			pids = append(pids, tr.Root.PID)
		}
		t.Fatalf("want 2 forgotten trees, got %d (pids %v)", len(got), pids)
	}
	// oldest-first
	if got[0].Root.PID != 100 || got[1].Root.PID != 600 {
		t.Fatalf("want roots [100, 600] oldest-first, got [%d, %d]", got[0].Root.PID, got[1].Root.PID)
	}

	root := got[0]
	if root.Procs != 3 {
		t.Errorf("tree size: want 3 (root + 2 descendants), got %d", root.Procs)
	}
	if root.Root.Cwd != "/proj" {
		t.Errorf("launch directory: want /proj, got %q", root.Root.Cwd)
	}
	if len(root.Ports) != 1 || root.Ports[0].Number != 8766 {
		t.Errorf("ports: want [8766] aggregated from the tree, got %+v", root.Ports)
	}

	leaf := got[1]
	if leaf.Procs != 1 {
		t.Errorf("lone root tree size: want 1, got %d", leaf.Procs)
	}
	if len(leaf.Ports) != 0 {
		t.Errorf("lone root ports: want none, got %+v", leaf.Ports)
	}
}

// A descendant that sits in a *different* dead group also satisfies the two
// conditions on its own; it must still be reported as part of its ancestor's
// tree rather than as a second finding, or its processes are counted twice.
func TestDetectDoesNotNestRoots(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	nested := []proc.Process{
		{PID: 1, PPID: 0, PGID: 1, Cmdline: "/sbin/launchd"},
		{PID: 100, PPID: 1, PGID: 900, Name: "outer", Cmdline: "outer serve", Created: base},
		{PID: 101, PPID: 100, PGID: 910, Name: "inner", Cmdline: "inner worker", Created: base},
	}

	got := Detect(nested, nil)

	if len(got) != 1 {
		var pids []int32
		for _, tr := range got {
			pids = append(pids, tr.Root.PID)
		}
		t.Fatalf("want a single root (100), got %v", pids)
	}
	if got[0].Root.PID != 100 || got[0].Procs != 2 {
		t.Errorf("want root 100 with 2 processes, got root %d with %d", got[0].Root.PID, got[0].Procs)
	}
}

func TestApplyIgnore(t *testing.T) {
	trees := []Tree{
		{Root: proc.Process{PID: 1, Name: "uv"}},
		{Root: proc.Process{PID: 2, Name: "gbrain"}},
		{Root: proc.Process{PID: 3, Name: "some.helper"}},
	}

	// exact name and glob both drop; order of the rest is kept
	got, err := ApplyIgnore(trees, []string{"gbrain", "*.helper"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Root.PID != 1 {
		t.Fatalf("want only pid 1 kept, got %+v", got)
	}

	all, err := ApplyIgnore(trees, nil)
	if err != nil || len(all) != 3 {
		t.Errorf("nil patterns: want all 3 kept and no error, got %d err=%v", len(all), err)
	}

	if _, err := ApplyIgnore(trees, []string{"["}); err == nil {
		t.Error("want an error for the malformed pattern \"[\", got nil")
	}
}

func TestDetectExcludes(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := Detect(procs(base), nil)

	flagged := make(map[int32]bool, len(got))
	for _, tr := range got {
		flagged[tr.Root.PID] = true
	}
	for _, tc := range []struct {
		pid int32
		why string
	}{
		{200, "a daemon leading its own live process group"},
		{301, "a helper whose parent is alive in the same process group"},
		{400, "a system-path daemon"},
		{500, "a GUI app-bundle helper"},
		{300, "a shell"},
		{101, "a descendant of a forgotten root"},
	} {
		if flagged[tc.pid] {
			t.Errorf("pid %d (%s) should not be reported as forgotten", tc.pid, tc.why)
		}
	}
}
