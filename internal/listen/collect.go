package listen

import (
	"os"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Collect gathers the current user's TCP listeners on the host via gopsutil
// (macOS uses lsof under the hood; Linux reads /proc), keeping only sockets in
// LISTEN state owned by the current user, and groups them by process.
func Collect() ([]Listener, error) {
	conns, err := gnet.Connections("tcp")
	if err != nil {
		return nil, err
	}

	var records []Record
	pids := make(map[int32]bool)
	for _, c := range conns {
		if c.Status != "LISTEN" || c.Pid == 0 {
			continue
		}
		records = append(records, Record{PID: c.Pid, IP: c.Laddr.IP, Port: c.Laddr.Port})
		pids[c.Pid] = true
	}

	me := int32(os.Getuid())
	meta := make(map[int32]Meta, len(pids))
	for pid := range pids {
		p, err := process.NewProcess(pid)
		if err != nil {
			continue
		}
		uids, err := p.Uids()
		if err != nil || len(uids) == 0 || int32(uids[0]) != me {
			continue // can't read it, or it belongs to another user
		}
		name, _ := p.Name()
		user, _ := p.Username()
		cmdline, _ := p.Cmdline()
		var created time.Time
		if ms, err := p.CreateTime(); err == nil && ms > 0 {
			created = time.UnixMilli(ms)
		}
		meta[pid] = Meta{UID: int32(uids[0]), User: user, Name: name, Cmdline: cmdline, Created: created}
	}

	// keep only records whose process passed the current-user filter
	kept := records[:0]
	for _, r := range records {
		if _, ok := meta[r.PID]; ok {
			kept = append(kept, r)
		}
	}
	return Assemble(kept, meta), nil
}
