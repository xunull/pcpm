package cmd

import (
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/proc"
	"github.com/xunull/pcpm/internal/top"
)

// Privilege is itself the switch: there is no flag, because a flag could not
// grant the access it would be asking for (ADR-0011).
//
// This covers the decision without being root. Whether root can *in fact* read
// another user's counters is a property of the operating system, not of this
// code, and has to be confirmed by running under sudo.
func TestRootRanksEveryoneAndAnyoneElseRanksThemselves(t *testing.T) {
	root := ownerForUID(0)
	if !root.Covers(proc.Process{UID: 501}) || !root.Covers(proc.Process{UID: 0}) {
		t.Error("running as root should rank every process")
	}

	me := ownerForUID(501)
	if !me.Covers(proc.Process{UID: 501}) {
		t.Error("a user should rank their own processes")
	}
	if me.Covers(proc.Process{UID: 0}) {
		t.Error("an unprivileged ranking must exclude another user's processes, "+
			"whose counters read zero without erroring", top.AnyOwner())
	}
}

// clockMachine records how long rankOnce actually waited between its two reads.
type clockMachine struct {
	reads []time.Time
}

func (m *clockMachine) Readings() ([]top.Reading, error) {
	m.reads = append(m.reads, time.Now())
	return []top.Reading{{PID: 1, CPUSeconds: float64(len(m.reads))}}, nil
}

func (m *clockMachine) Describe(pid int32) (proc.Process, error) {
	return proc.Process{PID: pid, Name: "p"}, nil
}

func (m *clockMachine) System() (top.SystemReading, error) {
	return top.SystemReading{Cores: 1}, nil
}

// The one-shot path is the one a longer Interval costs the most — a script
// waiting on `-o json` pays it every run — so it must be the configured
// Interval it waits, not a figure of its own.
func TestAOneShotWaitsTheIntervalItWasGiven(t *testing.T) {
	m := &clockMachine{}
	const interval = 300 * time.Millisecond

	if _, err := rankOnce(m, nil, interval, top.Options{}); err != nil {
		t.Fatal(err)
	}

	if len(m.reads) != 2 {
		t.Fatalf("read the machine %d times; a rate needs exactly two", len(m.reads))
	}
	if waited := m.reads[1].Sub(m.reads[0]); waited < interval {
		t.Errorf("waited %v between readings, less than the %v it was given", waited, interval)
	}
}
