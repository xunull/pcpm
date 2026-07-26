package watch

import (
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/proc"
)

func TestRunning(t *testing.T) {
	started := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	ix := proc.NewIndex([]proc.Process{
		{PID: 100, Created: started},
		{PID: 200, Created: started.Add(72 * time.Hour)}, // the PID was recycled
	})

	for _, tc := range []struct {
		name   string
		target Target
		want   bool
	}{
		{"the process is still there", Target{PID: 100, Created: started}, true},
		{"the process has exited", Target{PID: 300, Created: started}, false},
		{
			"the PID is in use again by a different process",
			Target{PID: 200, Created: started},
			false,
		},
	} {
		if got := tc.target.Running(ix); got != tc.want {
			t.Errorf("%s: Running = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWatching(t *testing.T) {
	stopped := time.Now()
	if !(Target{}).Watching() {
		t.Error("a target with no stop time should still be watched")
	}
	if (Target{StoppedAt: &stopped}).Watching() {
		t.Error("a stopped target should not be reported as watched")
	}
}
