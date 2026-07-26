package watch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonState is what can be learned about the collector daemon without talking
// to it: whether one is running, and when it last did any work.
type DaemonState struct {
	PID     int32
	Running bool
	// LastTick is when the daemon last recorded a sample, or zero if it never
	// has. A daemon that is running but has not ticked recently is worth
	// noticing, which is why this is reported rather than just liveness.
	LastTick time.Time
}

// lockFile is the daemon's single-instance marker, kept beside the database so
// one database has exactly one collector.
func lockFile(dbPath string) string {
	return dbPath + ".daemon"
}

// AcquireDaemonLock claims the right to be the collector for this database,
// returning a release function.
//
// The claim is the file's existence plus a live PID inside it. A lock left by a
// daemon that was killed ungracefully is therefore not fatal: the PID in it is
// checked, and a stale one is taken over rather than blocking every future
// start.
func AcquireDaemonLock(dbPath string) (release func(), err error) {
	path := lockFile(dbPath)
	if held, holder := daemonHolder(path); held {
		return nil, fmt.Errorf("a pcpm collector is already running for this database (pid %d)", holder)
	}
	// O_TRUNC rather than O_EXCL: we have just established that any existing
	// lock is stale, and refusing to overwrite it would strand the user.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("claiming the collector lock: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return func() { os.Remove(path) }, nil
}

// daemonHolder reports whether a live process holds the lock, and which.
func daemonHolder(path string) (bool, int32) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return false, 0
	}
	// Signal 0 asks the kernel whether the process exists without disturbing
	// it. A lock naming a dead PID is stale.
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false, 0
	}
	return true, int32(pid)
}

// Daemon reports on the collector for this database.
func (s *Store) Daemon(dbPath string) DaemonState {
	state := DaemonState{}
	if running, pid := daemonHolder(lockFile(dbPath)); running {
		state.Running, state.PID = true, pid
	}
	var ms int64
	// A single MAX over the sample table is enough: any tick that stored
	// anything moves it.
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(at), 0) FROM sample`).Scan(&ms); err == nil && ms > 0 {
		state.LastTick = time.UnixMilli(ms)
	}
	return state
}

// StopDaemon asks a running collector to stop, and reports whether there was
// one. Stopping when none runs is not an error: the caller wanted no collector,
// and there is none.
func StopDaemon(dbPath string) (bool, error) {
	running, pid := daemonHolder(lockFile(dbPath))
	if !running {
		return false, nil
	}
	// SIGTERM, not SIGKILL: the daemon stops between ticks, so the database is
	// never left mid-write.
	if err := syscall.Kill(int(pid), syscall.SIGTERM); err != nil {
		return false, fmt.Errorf("signalling the collector (pid %d): %w", pid, err)
	}
	return true, nil
}

// StartDaemon launches a background collector for this database, unless one is
// already running. It returns the new daemon's PID, or 0 when one was already
// there.
//
// The child is given its own session with setsid. That is not incidental: by
// ADR-0005's rule, leading its own process group makes the daemon structurally
// impossible for `pcpm forgotten` to report — the same property that keeps
// redis and postgres out of the findings. A tool that hunts unattended
// background processes must not leave one behind.
func StartDaemon(dbPath string) (int32, error) {
	if running, _ := daemonHolder(lockFile(dbPath)); running {
		return 0, nil
	}
	self, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding the pcpm binary: %w", err)
	}

	args := []string{"watch", "daemon", "--db", dbPath, "--quiet"}
	return startDetached(self, args, DaemonLogPath(dbPath))
}

// startDetached launches a process in its own session and returns its PID,
// having given up any claim on it.
//
// Setsid is what detaches it: the child leads a new session and process group,
// so it survives the terminal that started it and — by ADR-0005's rule — is
// structurally invisible to `pcpm forgotten`.
func startDetached(name string, args []string, logPath string) (int32, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil

	// The child outlives this terminal, so its output cannot go to one. A log
	// beside the database keeps a failure discoverable instead of silent.
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("opening %s: %w", logPath, err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %s: %w", filepath.Base(name), err)
	}
	// Read the PID before releasing: Release sets Process.Pid to -1, and a
	// caller that reported that number would be telling the user nothing.
	pid := int32(cmd.Process.Pid)
	// Nothing waits on the child, so release it rather than leave a zombie for
	// this process's brief remaining lifetime.
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}

// DaemonLogPath is where a background collector's output goes: it outlives the
// terminal that started it, so a failure has to be discoverable somewhere.
func DaemonLogPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "daemon.log")
}
