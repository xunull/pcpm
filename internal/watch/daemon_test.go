package watch

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestAcquireDaemonLockRefusesASecondDaemon(t *testing.T) {
	db := filepath.Join(t.TempDir(), "pcpm.db")

	release, err := AcquireDaemonLock(db)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	if _, err := AcquireDaemonLock(db); err == nil {
		t.Error("a second collector claimed the same database; want a clear refusal")
	}
}

func TestReleasingTheLockLetsTheNextDaemonStart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "pcpm.db")

	release, err := AcquireDaemonLock(db)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release()

	release2, err := AcquireDaemonLock(db)
	if err != nil {
		t.Fatalf("acquiring after release: %v", err)
	}
	release2()
}

// A daemon killed with SIGKILL never runs its release, so the lock file stays
// behind. It must not block every future start.
func TestAStaleLockIsTakenOver(t *testing.T) {
	db := filepath.Join(t.TempDir(), "pcpm.db")

	// PID 1 is alive but is not us; use a PID that cannot be running instead.
	if err := os.WriteFile(lockFile(db), []byte("2147483646\n"), 0o644); err != nil {
		t.Fatalf("planting a stale lock: %v", err)
	}

	release, err := AcquireDaemonLock(db)
	if err != nil {
		t.Fatalf("a lock naming a dead process should be taken over, got: %v", err)
	}
	release()
}

func TestAGarbledLockIsTakenOver(t *testing.T) {
	db := filepath.Join(t.TempDir(), "pcpm.db")
	if err := os.WriteFile(lockFile(db), []byte("not a pid"), 0o644); err != nil {
		t.Fatalf("planting a garbled lock: %v", err)
	}

	release, err := AcquireDaemonLock(db)
	if err != nil {
		t.Fatalf("an unreadable lock should be taken over, got: %v", err)
	}
	release()
}

func TestDaemonStateReportsLivenessAndLastTick(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "pcpm.db")
	s, err := Open(db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if got := s.Daemon(db); got.Running {
		t.Error("no collector has started; want Running false")
	}

	release, err := AcquireDaemonLock(db)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	state := s.Daemon(db)
	if !state.Running {
		t.Error("a held lock should report a running collector")
	}
	if state.PID != int32(os.Getpid()) {
		t.Errorf("PID = %d, want this process (%d)", state.PID, os.Getpid())
	}
	if !state.LastTick.IsZero() {
		t.Errorf("nothing has been collected yet; want a zero LastTick, got %v", state.LastTick)
	}

	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tgt, _ := s.AddTarget(target(100, created), created)
	tick := created.Add(time.Minute)
	if err := s.SaveSamples(tgt.ID, tick, []Sample{{PID: 100, Created: created}}); err != nil {
		t.Fatalf("SaveSamples: %v", err)
	}
	if got := s.Daemon(db).LastTick; !got.Equal(tick) {
		t.Errorf("LastTick = %v, want %v", got, tick)
	}
}

func TestStopDaemonWhenNoneIsRunning(t *testing.T) {
	db := filepath.Join(t.TempDir(), "pcpm.db")

	stopped, err := StopDaemon(db)
	if err != nil {
		t.Fatalf("stopping when none runs should not error: %v", err)
	}
	if stopped {
		t.Error("reported stopping a collector that was not running")
	}
}

// os.Process.Release sets Pid to -1, so reading the PID after releasing reports
// a number that means nothing. The caller uses it to tell the user which
// background process pcpm just started, so it has to be the real one.
func TestStartDetachedReturnsTheRealPID(t *testing.T) {
	dir := t.TempDir()

	pid, err := startDetached("/bin/sleep", []string{"1"}, filepath.Join(dir, "out.log"))
	if err != nil {
		t.Fatalf("startDetached: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("startDetached returned pid %d; want the child's real PID", pid)
	}
	// signal 0 asks whether it exists, without disturbing it
	if err := syscall.Kill(int(pid), 0); err != nil {
		t.Errorf("no process with the returned pid %d: %v", pid, err)
	}
	syscall.Kill(int(pid), syscall.SIGTERM)
}

// The detached child must lead its own process group, which is what keeps it
// alive past the terminal and out of `pcpm forgotten` (ADR-0005).
func TestStartDetachedLeadsItsOwnProcessGroup(t *testing.T) {
	dir := t.TempDir()

	pid, err := startDetached("/bin/sleep", []string{"2"}, filepath.Join(dir, "out.log"))
	if err != nil {
		t.Fatalf("startDetached: %v", err)
	}
	defer syscall.Kill(int(pid), syscall.SIGTERM)

	pgid, err := syscall.Getpgid(int(pid))
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if int32(pgid) != pid {
		t.Errorf("PGID %d != PID %d: the child did not lead its own group, so pcpm forgotten could report it", pgid, pid)
	}
	if pgid == syscall.Getpgrp() {
		t.Error("the child stayed in this process's group; it is not detached")
	}
}
