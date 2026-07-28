package cmd

import (
	"testing"

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
