package render

import (
	"fmt"
	"strings"
)

// Column is one slice of time in a chart: what the tree averaged over it, what
// it peaked at, and whether anything was collected at all.
//
// Present separates "idle" from "not collected". They are different facts, and
// a chart that drew them the same would let a stopped collector pass for a
// quiet process.
type Column struct {
	Value   float64
	Peak    float64
	Present bool
}

// AreaOptions is how a filled chart should be drawn.
type AreaOptions struct {
	Width  int     // character columns
	Height int     // character rows
	Max    float64 // the value at the top of the chart
	// Flat draws in one colour instead of the vertical gradient.
	//
	// The gradient reads as severity, and it only tells the truth when the
	// axis means something absolute — btop can colour by row because its scale
	// is always 0–100% of a CPU. A chart fitted to whatever its own data
	// happened to reach has no such meaning, and colouring the top red would
	// call a process using 8% of a core "critical".
	Flat bool
}

// brailleFill maps (left sub-column level, right sub-column level), each 0–4,
// to the character filling both from the bottom. Taken from btop's
// `braille_up` table (src/btop_draw.cpp, v1.4.7): a braille cell is two dots
// wide and four tall, which is what lets one character carry two time steps and
// a peak sit one dot above its fill.
var brailleFill = [5][5]rune{
	{' ', '⢀', '⢠', '⢰', '⢸'},
	{'⡀', '⣀', '⣠', '⣰', '⣸'},
	{'⡄', '⣄', '⣤', '⣴', '⣼'},
	{'⡆', '⣆', '⣦', '⣶', '⣾'},
	{'⡇', '⣇', '⣧', '⣷', '⣿'},
}

// subRows is how many dot rows a braille character holds.
const subRows = 4

// Area renders columns as a filled chart, two columns per character.
//
// Columns are laid out from the right, so a window holding fewer of them than
// the chart is wide is padded on the left rather than stretched across it —
// stretching leaves a blank between every pair of points, which is what turns a
// line into a row of dashes.
func Area(columns []Column, o AreaOptions) string {
	if o.Width < 1 {
		o.Width = 1
	}
	if o.Height < 1 {
		o.Height = 1
	}
	if o.Max <= 0 {
		o.Max = 100
	}

	// Keep the newest columns when there are more than fit.
	capacity := o.Width * 2
	if len(columns) > capacity {
		columns = columns[len(columns)-capacity:]
	}
	// Right-align: sub-column index of the first datum.
	pad := capacity - len(columns)

	levels := make([][2]int, o.Width*o.Height) // [row*width+col] = {left, right}
	peaks := make([][2]int, o.Width*o.Height)
	for i, c := range columns {
		if !c.Present {
			continue
		}
		sub := pad + i
		col, side := sub/2, sub%2

		fill := dots(c.Value, o.Max, o.Height)
		// An idle period still marks the baseline, so that it reads as "nothing
		// was happening" rather than "nothing was recorded".
		if fill < 1 {
			fill = 1
		}
		for h := range fill {
			row := o.Height - 1 - h/subRows
			at := row*o.Width + col
			if level := h%subRows + 1; level > levels[at][side] {
				levels[at][side] = level
			}
		}
		// The cap sits at the peak's own height, one dot thick: a compressed
		// bucket has to keep the evidence that something happened in it.
		if c.Peak > c.Value {
			h := dots(c.Peak, o.Max, o.Height) - 1
			if h >= fill {
				row := o.Height - 1 - h/subRows
				at := row*o.Width + col
				if level := h%subRows + 1; level > peaks[at][side] {
					peaks[at][side] = level
				}
			}
		}
	}

	var b strings.Builder
	for row := range o.Height {
		// Colour comes from the row, not the value: a fixed vertical gradient
		// means a busy period reads as a shape, without going to the axis to
		// find out how high it got. btop does the same.
		colour := green
		if !o.Flat {
			colour = rowColour(row, o.Height)
		}
		for col := range o.Width {
			at := row*o.Width + col
			cell := brailleFill[levels[at][0]][levels[at][1]]
			if p := peaks[at]; p[0] > 0 || p[1] > 0 {
				cell = capRune(p[0], p[1])
			}
			if cell == ' ' {
				b.WriteByte(' ')
				continue
			}
			fmt.Fprintf(&b, "\x1b[%dm%c\x1b[0m", colour, cell)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// dots converts a value into how many dot rows it fills, clamped to the chart.
// A value above Max fills to the top rather than being dropped: a tree can use
// more than one core, and a chart that silently clipped it would hide that.
func dots(value, max float64, height int) int {
	if value <= 0 {
		return 0
	}
	total := height * subRows
	n := int(value/max*float64(total) + 0.5)
	if n > total {
		n = total
	}
	return n
}

// capRune draws the peak marker: the topmost dot of each sub-column, without
// the fill beneath it, so it reads as a cap rather than as more area.
func capRune(left, right int) rune {
	const base = 0x2800
	// dot bits within a braille cell, top to bottom, for each sub-column
	bits := [subRows][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}
	var r rune
	if left > 0 {
		r |= bits[subRows-left][0]
	}
	if right > 0 {
		r |= bits[subRows-right][1]
	}
	if r == 0 {
		return ' '
	}
	return base + r
}

// rowColour is the vertical gradient: green low, yellow in the middle, red at
// the top. Only the row matters, so the same height always reads the same way.
const (
	green  = 32
	yellow = 33
	red    = 31
)

func rowColour(row, height int) int {
	if height < 2 {
		return green
	}
	// fraction of the way up the chart, 0 at the bottom
	up := float64(height-1-row) / float64(height-1)
	switch {
	case up > 0.75:
		return red
	case up > 0.45:
		return yellow
	}
	return green
}
