// Package forgotten holds the domain logic for pcpm's primary lens: processes
// nothing is looking after any more — the surviving roots of jobs that were
// never cleaned up. See CONTEXT.md ("Forgotten Process") and docs/adr/0005.
package forgotten

import (
	"cmp"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/xunull/pcpm/internal/listen"
)

// Process is a single process observed on the host, reduced to the fields pcpm
// needs to decide whether its launching job is gone.
type Process struct {
	PID     int32
	PPID    int32
	PGID    int32 // process group; a setsid'd daemon has PGID == PID
	UID     int32
	User    string
	Name    string
	Cmdline string
	Cwd     string // launch directory
	Created time.Time
}

// Tree is a Forgotten Process together with what its descendants add: the whole
// tree was forgotten, not just the root.
type Tree struct {
	Root  Process
	Procs int           // processes in the tree, including the root
	Ports []listen.Port // listening ports held anywhere in the tree, ascending
}

// Detect returns the forgotten process trees, oldest-first. portsByPID supplies
// the listening ports of any process, so a root inherits the ports its
// descendants hold; it may be nil.
func Detect(procs []Process, portsByPID map[int32][]listen.Port) []Tree {
	byPID := make(map[int32]Process, len(procs))
	children := make(map[int32][]int32, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p.PID)
	}

	var out []Tree
	for _, p := range procs {
		if !isForgotten(p, byPID) {
			continue
		}
		members := treeMembers(p.PID, children)
		out = append(out, Tree{
			Root:  p,
			Procs: len(members),
			Ports: collectPorts(members, portsByPID),
		})
	}
	slices.SortStableFunc(out, func(a, b Tree) int { return a.Root.Created.Compare(b.Root.Created) })
	return out
}

// ApplyIgnore returns the trees whose root process name matches none of the
// glob patterns (path.Match syntax, e.g. "gbrain", "*.helper"). This is how a
// user silences a long-running job they keep on purpose. A malformed pattern is
// reported as an error rather than silently ignored.
func ApplyIgnore(trees []Tree, patterns []string) ([]Tree, error) {
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("invalid ignore pattern %q: %w", pattern, err)
		}
	}
	if len(patterns) == 0 {
		return trees, nil
	}
	out := make([]Tree, 0, len(trees))
	for _, t := range trees {
		if !matchesAny(t.Root.Name, patterns) {
			out = append(out, t)
		}
	}
	return out, nil
}

// matchesAny reports whether name matches any of the glob patterns, which
// ApplyIgnore has already validated.
func matchesAny(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// isForgotten reports whether p is the surviving root of a job nobody cleaned
// up. Two conditions, both required:
//
//  1. p's process group leader is dead — the job that launched it is gone. A
//     well-behaved daemon calls setsid() and leads its own group, so its leader
//     is itself and it is never flagged.
//  2. p's parent is not in that same process group — p is the boundary where
//     the orphaning happened, rather than a descendant inside the leftover tree
//     (whose parent is alive and shares the dead group).
func isForgotten(p Process, byPID map[int32]Process) bool {
	if _, leaderAlive := byPID[p.PGID]; leaderAlive {
		return false
	}
	if parent, ok := byPID[p.PPID]; ok && parent.PGID == p.PGID {
		return false
	}
	return !isNoise(p.Cmdline)
}

// systemPrefixes are executable paths owned by the OS: daemons launched by the
// init system, not by anything the user was working in.
var systemPrefixes = []string{
	"/System/", "/usr/libexec/", "/usr/sbin/", "/usr/bin/", "/sbin/",
	"/Library/Apple/", "/usr/local/libexec/",
}

// shellPattern matches an interactive shell. A leftover shell is a closed
// terminal's remains, not a job someone forgot to stop.
var shellPattern = regexp.MustCompile(`^-?(?:/opt/homebrew/bin/|/usr/local/bin/|/bin/|/usr/bin/)?(?:zsh|bash|sh|fish|tcsh|csh|dash|login)\b`)

// isNoise reports whether a command line belongs to a category that is always
// parented oddly yet never actually forgotten: OS daemons, GUI app helpers
// (managed by their app over IPC), and shells.
func isNoise(cmdline string) bool {
	c := strings.TrimSpace(cmdline)
	for _, prefix := range systemPrefixes {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	if strings.Contains(c, "/Applications/") && strings.Contains(c, ".app/Contents/") {
		return true
	}
	return shellPattern.MatchString(c)
}

// treeMembers returns root and every descendant of it, breadth-first.
func treeMembers(root int32, children map[int32][]int32) []int32 {
	members := []int32{root}
	seen := map[int32]bool{root: true}
	for i := 0; i < len(members); i++ {
		for _, kid := range children[members[i]] {
			if !seen[kid] {
				seen[kid] = true
				members = append(members, kid)
			}
		}
	}
	return members
}

// collectPorts merges the listening ports of every process in a tree, deduped
// by port number (a port seen as exposed anywhere stays exposed) and ascending.
func collectPorts(members []int32, portsByPID map[int32][]listen.Port) []listen.Port {
	exposed := make(map[uint32]bool)
	for _, pid := range members {
		for _, port := range portsByPID[pid] {
			exposed[port.Number] = exposed[port.Number] || port.Exposed
		}
	}
	if len(exposed) == 0 {
		return nil
	}
	ports := make([]listen.Port, 0, len(exposed))
	for number, isExposed := range exposed {
		ports = append(ports, listen.Port{Number: number, Exposed: isExposed})
	}
	slices.SortFunc(ports, func(a, b listen.Port) int { return cmp.Compare(a.Number, b.Number) })
	return ports
}
