package forgotten

import (
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Collect enumerates every process on the host, reducing each to a Process.
// Entries whose parent or process group can't be read — the process exited
// mid-scan, or we lack permission — are skipped rather than failing the scan.
// The process group comes from syscall.Getpgid, which gopsutil does not expose.
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
		// A process whose group can't be read is treated as leading its own
		// group: that keeps it out of the findings and, more importantly, keeps
		// it present as a live leader so its group members aren't misread as
		// having lost theirs.
		pgid, err := syscall.Getpgid(int(p.Pid))
		if err != nil {
			pgid = int(p.Pid)
		}
		var uid int32
		if uids, err := p.Uids(); err == nil && len(uids) > 0 {
			uid = int32(uids[0])
		}
		user, _ := p.Username()
		name, _ := p.Name()
		cmdline, _ := p.Cmdline()
		cwd, _ := p.Cwd()

		var created time.Time
		if ms, err := p.CreateTime(); err == nil && ms > 0 {
			created = time.UnixMilli(ms)
		}

		out = append(out, Process{
			PID:     p.Pid,
			PPID:    ppid,
			PGID:    int32(pgid),
			UID:     uid,
			User:    user,
			Name:    name,
			Cmdline: cmdline,
			Cwd:     cwd,
			Created: created,
		})
	}
	return out, nil
}
