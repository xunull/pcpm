package watch

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// feedAll pushes lines through an accumulator, failing the test on any error.
func feedAll(t *testing.T, a *accumulator, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if err := a.feed(l); err != nil {
			t.Fatalf("feed(%q): %v", l, err)
		}
	}
}

// The first reading of a process establishes where it started. Treating it as a
// delta from zero would credit the process with everything it had transferred
// before pcpm was watching — a spike at the moment it was noticed.
func TestFirstSightingSeedsRatherThanCounts(t *testing.T) {
	a := newAccumulator()

	feedAll(t, a, "python3.11072,5000000,3000000,")

	if got := a.snapshot()[11072]; got != (Traffic{}) {
		t.Errorf("first sighting contributed %+v, want nothing", got)
	}
}

func TestSubsequentReadingsContributeTheDifference(t *testing.T) {
	a := newAccumulator()

	feedAll(t, a,
		"python3.11072,1000,500,",
		"python3.11072,3000,900,",
		"python3.11072,3500,900,",
	)

	want := Traffic{InBytes: 2500, OutBytes: 400}
	if got := a.snapshot()[11072]; got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// nettop's per-process figure is a sum over the sockets it is still tracking, so
// it falls when a connection closes. A fall is not negative traffic; it is a new
// baseline.
func TestAFallingCounterContributesNothingAndRebaselines(t *testing.T) {
	a := newAccumulator()

	feedAll(t, a,
		"bun.100,10000,0,",
		"bun.100,12000,0,", // +2000
		"bun.100,4000,0,",  // a connection closed — not -8000
		"bun.100,6000,0,",  // +2000
	)

	want := Traffic{InBytes: 4000}
	if got := a.snapshot()[100]; got != want {
		t.Errorf("snapshot = %+v, want %+v", got, want)
	}
}

// When the source process is restarted its counters begin again from zero. The
// first reading afterwards must re-seed, or the whole counter lands as a spike.
func TestARestartSeedsAgainInsteadOfSpiking(t *testing.T) {
	a := newAccumulator()

	feedAll(t, a, "bun.100,1000,0,", "bun.100,3000,0,")
	a.restart()
	feedAll(t, a, "bun.100,900000,0,", "bun.100,901000,0,")

	want := Traffic{InBytes: 3000} // 2000 before the restart, 1000 after
	if got := a.snapshot()[100]; got != want {
		t.Errorf("snapshot = %+v, want %+v — a restart must not spike", got, want)
	}
}

// nettop truncates the name to fifteen characters and joins it to the PID with a
// dot, so a name containing dots or spaces is ordinary rather than exceptional.
func TestPIDComesFromTheLastDot(t *testing.T) {
	a := newAccumulator()

	feedAll(t, a,
		"python3.13.11072,100,100,",
		"Adobe Desktop S.7057,100,100,",
		"Creative Cloud .7080,100,100,",
	)
	feedAll(t, a,
		"python3.13.11072,200,200,",
		"Adobe Desktop S.7057,300,300,",
		"Creative Cloud .7080,400,400,",
	)

	for pid, want := range map[int32]int64{11072: 100, 7057: 200, 7080: 300} {
		if got := a.snapshot()[pid].InBytes; got != want {
			t.Errorf("pid %d: %d bytes, want %d", pid, got, want)
		}
	}
}

func TestHeaderRowsAreSkipped(t *testing.T) {
	a := newAccumulator()

	feedAll(t, a, trafficHeader, "bun.100,1000,0,", trafficHeader, "bun.100,2000,0,")

	if got := a.snapshot()[100].InBytes; got != 1000 {
		t.Errorf("InBytes = %d, want 1000", got)
	}
}

// The output format carries no compatibility promise, and reading columns by
// position after they move produces confident wrong numbers.
func TestAnUnfamiliarHeaderIsRefused(t *testing.T) {
	a := newAccumulator()

	err := a.feed(",bytes_out,bytes_in,")

	if err == nil {
		t.Fatal("a reordered header was accepted")
	}
	if !strings.Contains(err.Error(), "bytes_in") {
		t.Errorf("the error should name what was expected, got %q", err)
	}
}

func TestRowsThatAreNotMeasurementsAreIgnored(t *testing.T) {
	a := newAccumulator()

	// blank lines, a connection detail row, and a row with no numbers
	feedAll(t, a,
		"",
		"tcp4 127.0.0.1:5000<->127.0.0.1:6000,,,",
		"bun.100,,,",
		"bun.100,1000,0,",
		"bun.100,2000,0,",
	)

	if got := a.snapshot()[100].InBytes; got != 1000 {
		t.Errorf("InBytes = %d, want 1000", got)
	}
}

// A process pcpm never saw again keeps whatever it accumulated; the counter is
// cumulative, so forgetting it would make the total fall.
func TestAProcessKeepsItsTotalAfterItStopsBeingReported(t *testing.T) {
	a := newAccumulator()

	feedAll(t, a, "bun.100,1000,0,", "bun.100,5000,0,")
	feedAll(t, a, "other.200,1,1,") // a later sample without pid 100

	if got := a.snapshot()[100].InBytes; got != 4000 {
		t.Errorf("InBytes = %d, want 4000 kept", got)
	}
}

// The snapshot is handed to the collector each tick; handing out the live map
// would let a later sample mutate figures already being written.
func TestSnapshotDoesNotAliasTheAccumulator(t *testing.T) {
	a := newAccumulator()
	feedAll(t, a, "bun.100,1000,0,", "bun.100,2000,0,")

	snap := a.snapshot()
	feedAll(t, a, "bun.100,9000,0,")

	if snap[100].InBytes != 1000 {
		t.Errorf("an earlier snapshot changed to %d", snap[100].InBytes)
	}
}

// --- the reader over a real child process --------------------------------

// fakeStream is a command that prints canned output, standing in for the
// measuring program so the reader can be tested without one.
func fakeStream(t *testing.T, lines ...string) *exec.Cmd {
	t.Helper()
	return exec.Command("sh", "-c", "printf '%s\\n' "+shellQuote(lines)+"; sleep 30")
}

func shellQuote(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("'" + strings.ReplaceAll(l, "'", `'\''`) + "'")
	}
	return b.String()
}

func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	for range 100 {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestReaderAccumulatesWhatTheProgramPrints(t *testing.T) {
	r, err := startReader(fakeStream(t,
		trafficHeader,
		"bun.100,1000,50,",
		trafficHeader,
		"bun.100,4000,90,",
	))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if !waitFor(t, func() bool { return r.Snapshot()[100].InBytes == 3000 }) {
		t.Errorf("snapshot = %+v, want 3000 in / 40 out", r.Snapshot()[100])
	}
	if err := r.Err(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// A header that has moved must stop the reading rather than be skipped: the
// columns after it can no longer be trusted by position.
func TestReaderStopsOnAnUnfamiliarHeader(t *testing.T) {
	r, err := startReader(fakeStream(t, ",bytes_out,bytes_in,", "bun.100,1000,50,"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if !waitFor(t, func() bool { return r.Err() != nil }) {
		t.Fatal("an unfamiliar header was accepted")
	}
	if got := r.Snapshot()[100]; got != (Traffic{}) {
		t.Errorf("figures were taken from an unrecognised format: %+v", got)
	}
}

// A source that exits is a failure the caller has to be able to see, because a
// silent stop looks exactly like a machine that went quiet.
func TestReaderReportsWhenTheProgramExits(t *testing.T) {
	r, err := startReader(exec.Command("sh", "-c", "printf '%s\\n' '"+trafficHeader+"'"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if !waitFor(t, func() bool { return r.Err() != nil }) {
		t.Error("the reader did not notice the program had stopped")
	}
}

func TestClosingTheReaderLeavesNothingRunning(t *testing.T) {
	r, err := startReader(fakeStream(t, trafficHeader))
	if err != nil {
		t.Fatal(err)
	}
	pid := r.cmd.Process.Pid
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// Signal 0 probes for existence without delivering anything.
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("pid %d is still running after Close", pid)
	}
}
