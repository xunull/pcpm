package proc

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
		described, err := describe(p)
		if err != nil {
			continue
		}
		out = append(out, described)
	}
	return out, nil
}

// Describe reads one process's facts. It is what Collect does for every process
// on the host, for callers that already know which process they mean — a
// ranking, for instance, re-reads measurements often but needs these once.
func Describe(pid int32) (Process, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return Process{}, err
	}
	return describe(p)
}

// describe reduces a gopsutil handle to a Process. A parent that cannot be read
// is the one hard failure: without it the process has no place in any tree.
func describe(p *process.Process) (Process, error) {
	ppid, err := p.Ppid()
	if err != nil {
		return Process{}, err
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
	exe, _ := p.Exe()

	var created time.Time
	if ms, err := p.CreateTime(); err == nil && ms > 0 {
		created = time.UnixMilli(ms)
	}

	return Process{
		PID:     p.Pid,
		PPID:    ppid,
		PGID:    int32(pgid),
		UID:     uid,
		User:    user,
		Name:    name,
		Cmdline: cmdline,
		Exe:     exe,
		Cwd:     cwd,
		Created: created,
	}, nil
}
