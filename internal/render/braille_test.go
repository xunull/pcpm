package render

import (
	"strings"
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/watch"
)

// plain drops the colour escapes so a frame's shape can be asserted on.
func plain(s string) string {
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

func TestAreaFillsFromTheBottom(t *testing.T) {
	cols := []Column{}
	for range 8 {
		cols = append(cols, Column{Value: 50, Present: true})
	}

	rows := plainRows(Area(cols, AreaOptions{Width: 4, Height: 4, Max: 100}))

	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(rows))
	}
	// half height: the bottom two rows carry the fill, the top two are empty
	if strings.TrimSpace(rows[0]) != "" || strings.TrimSpace(rows[1]) != "" {
		t.Errorf("a 50%% value should not reach the top half:\n%s", strings.Join(rows, "\n"))
	}
	if strings.TrimSpace(rows[3]) == "" {
		t.Errorf("the bottom row should be filled:\n%s", strings.Join(rows, "\n"))
	}
}

// Idle and "nothing was collected" are different facts and must look different.
// This is the distinction a monitor that never stops collecting can ignore, and
// pcpm's collector genuinely stops.
func TestZeroDrawsABaselineButMissingDataDoesNot(t *testing.T) {
	cols := []Column{
		{Value: 0, Present: true},
		{Value: 0, Present: true},
		{Present: false},
		{Present: false},
	}

	rows := plainRows(Area(cols, AreaOptions{Width: 2, Height: 3, Max: 100}))
	bottom := []rune(rows[len(rows)-1])

	if bottom[0] == ' ' {
		t.Error("an idle period should draw a baseline, not nothing")
	}
	if bottom[1] != ' ' {
		t.Errorf("a period with no data should be blank, got %q", string(bottom[1]))
	}
}

// btop's rule, and the fix for the dashes: a window holding fewer points than
// the chart has columns is packed to the right, never stretched across it.
func TestShortDataPadsTheLeftRatherThanStretching(t *testing.T) {
	// 6 sub-columns of data for a 10-wide chart (which holds 20)
	cols := make([]Column, 6)
	for i := range cols {
		cols[i] = Column{Value: 80, Present: true}
	}

	rows := plainRows(Area(cols, AreaOptions{Width: 10, Height: 3, Max: 100}))
	bottom := rows[len(rows)-1]

	leading := len(bottom) - len(strings.TrimLeft(bottom, " "))
	if leading != 7 {
		t.Errorf("want 7 blank columns then 3 of data, got %d leading blanks in %q", leading, bottom)
	}
	if strings.HasSuffix(bottom, " ") {
		t.Errorf("the data should sit against the right edge: %q", bottom)
	}
}

// A cap marks the peak of a compressed bucket. Without it a three-second burst
// inside an idle hour averages to nothing, and "is anything still using this"
// becomes unanswerable.
func TestPeakIsDrawnAboveTheFill(t *testing.T) {
	cols := make([]Column, 8)
	for i := range cols {
		cols[i] = Column{Value: 10, Peak: 90, Present: true}
	}

	withPeak := plainRows(Area(cols, AreaOptions{Width: 4, Height: 6, Max: 100}))
	withoutPeak := plainRows(Area([]Column{
		{Value: 10, Present: true}, {Value: 10, Present: true},
		{Value: 10, Present: true}, {Value: 10, Present: true},
		{Value: 10, Present: true}, {Value: 10, Present: true},
		{Value: 10, Present: true}, {Value: 10, Present: true},
	}, AreaOptions{Width: 4, Height: 6, Max: 100}))

	// the peak must reach a row the fill alone never touches
	topWithPeak := firstNonBlank(withPeak)
	topWithout := firstNonBlank(withoutPeak)
	if topWithPeak >= topWithout {
		t.Errorf("the peak did not rise above the fill: peak reaches row %d, fill alone reaches %d",
			topWithPeak, topWithout)
	}
}

func TestAreaWithNoColumns(t *testing.T) {
	out := Area(nil, AreaOptions{Width: 6, Height: 3, Max: 100})
	rows := plainRows(out)
	if len(rows) != 3 {
		t.Fatalf("an empty chart should still occupy its height, got %d rows", len(rows))
	}
	for _, r := range rows {
		if strings.TrimSpace(r) != "" {
			t.Errorf("no data should draw nothing, got %q", r)
		}
	}
}

// The value scale is the caller's: a tree can exceed one core, so a chart that
// silently clamped at 100 would hide it.
func TestValuesAboveTheMaxClampToFullHeight(t *testing.T) {
	cols := []Column{{Value: 250, Present: true}, {Value: 250, Present: true}}

	rows := plainRows(Area(cols, AreaOptions{Width: 1, Height: 3, Max: 100}))

	if strings.TrimSpace(rows[0]) == "" {
		t.Errorf("a value above Max should fill to the top:\n%s", strings.Join(rows, "\n"))
	}
}

func plainRows(s string) []string {
	return strings.Split(strings.TrimSuffix(plain(s), "\n"), "\n")
}

func firstNonBlank(rows []string) int {
	for i, r := range rows {
		if strings.TrimSpace(r) != "" {
			return i
		}
	}
	return len(rows)
}

// A chart slot stands for a slice of time, and a point covers however many
// slots its bucket spans. Lighting only one slot per point leaves the rest
// blank — and blank means "nothing was collected", so a five-minute window on a
// wide terminal turns into a sieve even though every sample arrived on time.
func TestPointsCoverTheSlotsTheirBucketSpans(t *testing.T) {
	from := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)

	// 5 seconds apart, exactly as a default collector produces
	var points []watch.Point
	for i := range 60 {
		points = append(points, watch.Point{
			At:         from.Add(time.Duration(i) * 5 * time.Second),
			CPUPercent: 40,
		})
	}

	columns := chartColumns(points, CPUSeries, 80, from, to)

	absent := 0
	for _, c := range columns {
		if !c.Present {
			absent++
		}
	}
	// A steady collector leaves no holes; allow only the rounding at the edges.
	if absent > len(columns)/10 {
		t.Errorf("%d of %d slots are blank for an unbroken series — the chart claims data is missing",
			absent, len(columns))
	}
}

// A real gap must still read as one, or the fix above would paper over the very
// thing the Present flag exists to show.
func TestARealGapStaysBlank(t *testing.T) {
	from := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)

	var points []watch.Point
	for i := range 60 {
		at := from.Add(time.Duration(i) * 5 * time.Second)
		if i >= 20 && i < 40 { // 100 seconds with nothing collected
			continue
		}
		points = append(points, watch.Point{At: at, CPUPercent: 40})
	}

	columns := chartColumns(points, CPUSeries, 80, from, to)

	absent := 0
	for _, c := range columns {
		if !c.Present {
			absent++
		}
	}
	// 100s of a 300s window is a third of it
	if absent < len(columns)/5 {
		t.Errorf("only %d of %d slots are blank; a 100-second gap was filled in", absent, len(columns))
	}
}

// The plot is narrower than the chart by the width of the axis labels. Laying
// the columns out for the chart instead silently drops the oldest of them off
// the left edge while the time axis goes on claiming to start where they were —
// the chart would be missing data and saying otherwise.
func TestTheStartOfTheWindowIsNotDroppedOffTheLeft(t *testing.T) {
	from := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)

	// A marker at the very start of the window, nothing else until much later.
	points := []watch.Point{
		{At: from, CPUPercent: 90},
		{At: from.Add(50 * time.Minute), CPUPercent: 10},
	}

	out := plain(Chart(points, CPUSeries, ChartOptions{
		Width: 80, Height: 5, From: from, To: to, Label: Percent,
	}))

	rows := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	// find the plot rows: those beginning with the axis gutter
	var plotted []string
	for _, r := range rows {
		if i := strings.Index(r, "│"); i >= 0 {
			plotted = append(plotted, r[i+len("│"):])
		}
	}
	if len(plotted) == 0 {
		t.Fatalf("no plot rows found:\n%s", out)
	}
	for _, r := range plotted {
		if strings.TrimSpace(r) != "" && strings.TrimLeft(r, " ") == r {
			return // something is drawn in the very first column: the start survived
		}
	}
	t.Errorf("nothing is drawn at the start of the window; the oldest columns were dropped:\n%s", out)
}

// An interpolated slot is a guess about the value between two samples. Carrying
// a neighbour's peak across it would invent a burst nothing observed — and a
// cap is precisely the mark that says "something happened here".
func TestInterpolatedSlotsDoNotInventPeaks(t *testing.T) {
	from := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	to := from.Add(5 * time.Minute)

	// sparse samples where peak equals value, as an uncompressed bucket gives
	points := []watch.Point{
		{At: from, CPUPercent: 10, PeakCPUPercent: 10},
		{At: from.Add(2*time.Minute + 30*time.Second), CPUPercent: 90, PeakCPUPercent: 90},
		{At: from.Add(5*time.Minute - time.Second), CPUPercent: 10, PeakCPUPercent: 10},
	}

	for _, c := range chartColumns(points, CPUSeries, 60, from, to) {
		if !c.Present {
			continue
		}
		if c.Peak > c.Value+0.001 {
			t.Fatalf("an interpolated slot claims a peak of %.1f above its value of %.1f", c.Peak, c.Value)
		}
	}
}
