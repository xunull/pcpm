package orphan

import (
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Collect enumerates every process on the host via gopsutil and reduces each to
// a Process. Entries whose parent or owner can't be read — the process exited
// mid-scan, or we lack permission — are skipped rather than failing the scan.
func Collect() ([]Process, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(procs))
	for _, p := range procs {
		ppid, err := p.Ppid()
		if err != nil {
			continue
		}
		uids, err := p.Uids()
		if err != nil || len(uids) == 0 {
			continue
		}
		user, _ := p.Username()
		name, _ := p.Name()
		cmdline, _ := p.Cmdline()

		var created time.Time
		if ms, err := p.CreateTime(); err == nil && ms > 0 {
			created = time.UnixMilli(ms)
		}

		out = append(out, Process{
			PID:     p.Pid,
			PPID:    ppid,
			UID:     int32(uids[0]),
			User:    user,
			Name:    name,
			Cmdline: cmdline,
			Created: created,
		})
	}
	return out, nil
}
