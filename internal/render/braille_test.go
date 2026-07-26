package render

import (
	"strings"
	"testing"
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
