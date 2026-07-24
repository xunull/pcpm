package listen

import (
	"testing"
	"time"
)

func TestAssemble(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	records := []Record{
		{PID: 20, IP: "127.0.0.1", Port: 8767}, // newer process
		{PID: 10, IP: "0.0.0.0", Port: 5000},   // older, exposed
		{PID: 10, IP: "127.0.0.1", Port: 3000}, // older, localhost
		{PID: 10, IP: "::", Port: 5000},        // duplicate port 5000 (v6 any) -> deduped, stays exposed
	}
	meta := map[int32]Meta{
		10: {UID: 501, User: "quincy", Name: "node", Cmdline: "node server.js", Created: base},
		20: {UID: 501, User: "quincy", Name: "python", Cmdline: "python -m http.server", Created: base.Add(time.Hour)},
	}

	got := Assemble(records, meta)

	if len(got) != 2 {
		t.Fatalf("want 2 listeners, got %d: %+v", len(got), got)
	}
	// oldest-first: pid 10 (base) before pid 20 (base+1h)
	if got[0].PID != 10 || got[1].PID != 20 {
		t.Fatalf("want order [10, 20], got [%d, %d]", got[0].PID, got[1].PID)
	}

	l := got[0]
	if l.Name != "node" {
		t.Errorf("metadata not joined: Name = %q, want node", l.Name)
	}
	// ports deduped (5000 once) and sorted ascending: 3000, 5000
	if len(l.Ports) != 2 || l.Ports[0].Number != 3000 || l.Ports[1].Number != 5000 {
		t.Fatalf("pid10 ports: want [3000, 5000], got %+v", l.Ports)
	}
	if l.Ports[0].Exposed {
		t.Errorf("port 3000 (127.0.0.1) should not be exposed")
	}
	if !l.Ports[1].Exposed {
		t.Errorf("port 5000 (0.0.0.0/::) should be exposed")
	}
}
