package proc

import (
	"slices"
	"testing"
)

// tree models: launchd -> a two-level tree, plus an unrelated process that must
// never be pulled in.
func tree() []Process {
	return []Process{
		{PID: 1, PPID: 0},
		{PID: 100, PPID: 1},
		{PID: 101, PPID: 100},
		{PID: 102, PPID: 100},
		{PID: 103, PPID: 101},
		{PID: 500, PPID: 1}, // unrelated
	}
}

func TestLookup(t *testing.T) {
	ix := NewIndex(tree())

	got, ok := ix.Lookup(101)
	if !ok {
		t.Fatal("pid 101 is in the snapshot but Lookup said it was not")
	}
	if got.PPID != 100 {
		t.Errorf("PPID = %d, want 100", got.PPID)
	}

	// An absent PID is how a caller learns a process is gone — e.g. that a
	// process group's leader is dead.
	if _, ok := ix.Lookup(999); ok {
		t.Error("pid 999 is not in the snapshot but Lookup said it was")
	}
}

func TestTreeMembers(t *testing.T) {
	ix := NewIndex(tree())

	got := ix.TreeMembers(100)
	slices.Sort(got)
	want := []int32{100, 101, 102, 103}
	if !slices.Equal(got, want) {
		t.Errorf("TreeMembers(100) = %v, want %v (root plus every descendant)", got, want)
	}

	if got := ix.TreeMembers(103); !slices.Equal(got, []int32{103}) {
		t.Errorf("a leaf's tree is itself: got %v", got)
	}
}

// A process that is not in the snapshot still yields itself, so a caller that
// asks about a process which exited mid-scan gets a one-member tree rather than
// an empty one it has to special-case.
func TestTreeMembersOfAnAbsentPID(t *testing.T) {
	ix := NewIndex(tree())

	if got := ix.TreeMembers(999); !slices.Equal(got, []int32{999}) {
		t.Errorf("TreeMembers(999) = %v, want [999]", got)
	}
}

// Parent links come from the OS and are read one process at a time, so a
// snapshot can be internally inconsistent and describe a cycle. Walking it must
// terminate rather than hang the caller.
func TestTreeMembersTerminatesOnACycle(t *testing.T) {
	ix := NewIndex([]Process{
		{PID: 10, PPID: 11},
		{PID: 11, PPID: 10},
	})

	got := ix.TreeMembers(10)
	slices.Sort(got)
	if !slices.Equal(got, []int32{10, 11}) {
		t.Errorf("TreeMembers(10) = %v, want [10 11] visited once each", got)
	}
}
