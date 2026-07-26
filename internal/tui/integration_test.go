package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xunull/pcpm/internal/watch"
)

// TestRendersAFrameFromARealStore drives the view the way the runtime does,
// against a real database rather than a fake source. The unit tests cover the
// model's behaviour; this covers the wiring between it, the store's query
// layer, and the chart renderer — the seams a fake would hide.
func TestRendersAFrameFromARealStore(t *testing.T) {
	store, err := watch.Open(filepath.Join(t.TempDir(), "pcpm.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	target, err := store.AddTarget(watch.Target{
		PID: 100, Created: now.Add(-2 * time.Hour), Name: "bun",
		Cmdline: "bun run dev", Cwd: "/proj",
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("AddTarget: %v", err)
	}

	// Half an hour of a wrapper that does nothing and a worker that does the
	// work — the shape this tool exists to make visible.
	var wrapperCPU, workerCPU float64
	for i := range 360 {
		at := now.Add(-30*time.Minute + time.Duration(i)*5*time.Second)
		wrapperCPU += 0.001
		workerCPU += 3.5 // ~70% of a core
		err := store.SaveSamples(target.ID, at, []watch.Sample{
			{PID: 100, Created: now.Add(-2 * time.Hour), Name: "bun", CPUSeconds: wrapperCPU, RSSBytes: 30 << 20},
			{PID: 101, Created: now.Add(-2 * time.Hour), Name: "esbuild", CPUSeconds: workerCPU, RSSBytes: 280 << 20},
		})
		if err != nil {
			t.Fatalf("SaveSamples: %v", err)
		}
	}

	m := New(StoreSource{Store: store, Target: target}, "", 1)
	m.now = func() time.Time { return now }
	m.refresh = time.Millisecond
	m.width, m.height = 96, 32

	// Drive the load exactly as the runtime would, following the batch.
	var model tea.Model = m
	for _, msg := range runCmd(m.Init()) {
		if _, isTick := msg.(tickMsg); isTick {
			continue // would only schedule another refresh
		}
		model, _ = model.Update(msg)
	}

	frame := model.(Model).View()
	t.Logf("\n%s", frame)

	for _, want := range []string{"100", "bun", "cpu", "memory", "esbuild", "PID"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame is missing %q", want)
		}
	}
	// The worker's ~70% must be visible somewhere, and the wrapper must not be
	// mistaken for it.
	if !strings.Contains(frame, "70%") && !strings.Contains(frame, "69%") && !strings.Contains(frame, "71%") {
		t.Errorf("the worker's CPU is not reported anywhere in the frame")
	}
	// A chart, not an empty box: asciigraph's axis and line characters.
	if !strings.Contains(frame, "┤") || !strings.Contains(frame, "─") {
		t.Error("no chart was drawn")
	}
	// The frame must fit the terminal it was told about.
	for _, line := range strings.Split(frame, "\n") {
		if width := len([]rune(stripANSI(line))); width > 96 {
			t.Errorf("line is %d columns wide on a 96-column terminal: %q", width, line)
		}
	}
}

// stripANSI removes colour escapes so a line's real width can be measured.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
