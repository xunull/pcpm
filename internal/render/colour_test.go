package render

import (
	"strings"
	"testing"
)

func TestDetectDepth(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want Depth
	}{
		{"truecolor", map[string]string{"COLORTERM": "truecolor"}, DepthTrue},
		{"24bit spelling", map[string]string{"COLORTERM": "24bit"}, DepthTrue},
		{"256 colour terminal", map[string]string{"TERM": "xterm-256color"}, Depth256},
		// COLORTERM is not set by every terminal that supports it. Degrading to
		// 256 is safe — it renders, just with less precision.
		{"nothing declared", map[string]string{"TERM": "xterm"}, Depth256},
		{"a real tty", map[string]string{"TERM": "linux"}, Depth16},
		{"dumb terminal", map[string]string{"TERM": "dumb"}, DepthNone},
		// The NO_COLOR convention: any value, including empty, disables colour.
		{"NO_COLOR set", map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"}, DepthNone},
		{"NO_COLOR empty", map[string]string{"NO_COLOR": "", "COLORTERM": "truecolor"}, DepthNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := detectDepth(func(k string) (string, bool) {
				v, ok := tc.env[k]
				return v, ok
			})
			if got != tc.want {
				t.Errorf("depth = %v, want %v", got, tc.want)
			}
		})
	}
}

// The gradient runs green through yellow to red, so the ends must not be the
// same colour — a gradient that collapses says nothing about severity.
func TestGradientMovesAcrossItsRange(t *testing.T) {
	p := Palette{Depth: DepthTrue}

	low, mid, high := p.Gradient(0), p.Gradient(0.5), p.Gradient(1)
	if low == high {
		t.Error("the ends of the gradient are the same colour")
	}
	if mid == low || mid == high {
		t.Error("the middle of the gradient is not distinct from its ends")
	}
	for _, s := range []string{low, mid, high} {
		if !strings.HasPrefix(s, "\x1b[38;2;") {
			t.Errorf("a truecolor palette should emit 24-bit escapes, got %q", s)
		}
	}
}

func TestGradientClampsOutsideItsRange(t *testing.T) {
	p := Palette{Depth: DepthTrue}

	if p.Gradient(-5) != p.Gradient(0) {
		t.Error("a value below the range should render as the bottom of the gradient")
	}
	if p.Gradient(99) != p.Gradient(1) {
		t.Error("a value above the range should render as the top of the gradient")
	}
}

func TestPaletteDowngradesWithDepth(t *testing.T) {
	for _, tc := range []struct {
		depth  Depth
		prefix string
	}{
		{DepthTrue, "\x1b[38;2;"},
		{Depth256, "\x1b[38;5;"},
		{Depth16, "\x1b[9"},
	} {
		got := Palette{Depth: tc.depth}.Gradient(0.9)
		if !strings.HasPrefix(got, tc.prefix) {
			t.Errorf("depth %v: got %q, want it to start %q", tc.depth, got, tc.prefix)
		}
	}
}

// A terminal that cannot colour must get plain text, not escapes it will print
// literally.
func TestNoDepthEmitsNothing(t *testing.T) {
	p := Palette{Depth: DepthNone}

	if got := p.Gradient(0.9); got != "" {
		t.Errorf("Gradient = %q, want empty", got)
	}
	if got := p.Reset(); got != "" {
		t.Errorf("Reset = %q, want empty", got)
	}
}

// Structural elements inherit the terminal's own foreground. Choosing a colour
// for them is what made the first version's axis invisible on a dark theme.
func TestPaletteHasNoColourForStructure(t *testing.T) {
	body := Chart(nil, CPUSeries, ChartOptions{
		Width: 40, Height: 4, Title: "CPU", Label: Percent,
	})
	// the title, axis and labels are all outside any escape sequence
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "│") || strings.Contains(line, "└") || strings.Contains(line, "CPU") {
			if strings.Contains(line, "\x1b[") {
				t.Errorf("structural line carries a colour escape: %q", line)
			}
		}
	}
}

// Never paint a background: the terminal's own is the only one that is right.
func TestNothingSetsABackground(t *testing.T) {
	for _, d := range []Depth{DepthTrue, Depth256, Depth16} {
		p := Palette{Depth: d}
		for _, s := range []string{p.Gradient(0), p.Gradient(1), p.Reset()} {
			if strings.Contains(s, "48;") || strings.Contains(s, "\x1b[4") && !strings.Contains(s, "\x1b[49m") {
				t.Errorf("depth %v emits a background escape: %q", d, s)
			}
		}
	}
}
