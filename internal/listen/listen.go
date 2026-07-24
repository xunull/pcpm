// Package listen holds the domain logic for pcpm's primary lens: the current
// user's processes that hold a listening TCP socket (see CONTEXT.md, term
// "Listener", and docs/adr/0004).
package listen

import (
	"cmp"
	"slices"
	"time"
)

// Port is a TCP port a process listens on.
type Port struct {
	Number  uint32
	Exposed bool // bound to all interfaces (0.0.0.0 / :: / *) — reachable off this host
}

// Listener is a process the current user owns that holds one or more listening
// TCP sockets.
type Listener struct {
	PID     int32
	UID     int32
	User    string
	Name    string
	Cmdline string
	Created time.Time
	Ports   []Port
}

// Record is one raw listening socket: a bound address and the pid that owns it.
type Record struct {
	PID  int32
	IP   string
	Port uint32
}

// Meta is the process metadata for a pid.
type Meta struct {
	UID     int32
	User    string
	Name    string
	Cmdline string
	Created time.Time
}

// Assemble groups raw listening records by pid into Listeners: it dedupes ports,
// marks network-exposed ones, sorts each Listener's ports ascending, and sorts
// the Listeners oldest-first (earliest Created first). A pid with no metadata
// still appears, with zero-value fields.
func Assemble(records []Record, meta map[int32]Meta) []Listener {
	byPID := make(map[int32]*Listener)
	var order []int32
	seen := make(map[int32]map[uint32]bool)

	for _, r := range records {
		l, ok := byPID[r.PID]
		if !ok {
			m := meta[r.PID]
			l = &Listener{PID: r.PID, UID: m.UID, User: m.User, Name: m.Name, Cmdline: m.Cmdline, Created: m.Created}
			byPID[r.PID] = l
			order = append(order, r.PID)
			seen[r.PID] = make(map[uint32]bool)
		}
		exposed := isExposed(r.IP)
		if seen[r.PID][r.Port] {
			if exposed { // same port on both loopback and any-interface -> exposed wins
				for i := range l.Ports {
					if l.Ports[i].Number == r.Port {
						l.Ports[i].Exposed = true
					}
				}
			}
			continue
		}
		seen[r.PID][r.Port] = true
		l.Ports = append(l.Ports, Port{Number: r.Port, Exposed: exposed})
	}

	out := make([]Listener, 0, len(order))
	for _, pid := range order {
		l := byPID[pid]
		slices.SortFunc(l.Ports, func(a, b Port) int { return cmp.Compare(a.Number, b.Number) })
		out = append(out, *l)
	}
	slices.SortStableFunc(out, func(a, b Listener) int { return a.Created.Compare(b.Created) })
	return out
}

// isExposed reports whether a bound IP means "all interfaces" (reachable off-host).
func isExposed(ip string) bool {
	switch ip {
	case "", "*", "0.0.0.0", "::":
		return true
	default:
		return false
	}
}
