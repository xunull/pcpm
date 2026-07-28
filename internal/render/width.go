package render

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Align says how a cell sits inside the width its column was given.
type Align int

const (
	Left  Align = iota // the default, and what every column was before
	Right              // for quantities, whose digits only compare when they line up
)

// displayWidth is how many terminal columns s occupies.
//
// This is a third number, agreeing with neither of the two obvious ones: "汽水
// 音乐" is twelve bytes, four runes, and eight columns. Laying out a table by
// either of the others pushes every column after it out of true — by a third
// for bytes, by double for runes.
func displayWidth(s string) int { return runewidth.StringWidth(s) }

// padTo returns s filled out to width columns. A cell already at least that
// wide is returned unchanged: the column has grown to fit it, and clipping it
// here would hide what the width was computed from.
func padTo(s string, width int, a Align) string {
	pad := width - displayWidth(s)
	if pad <= 0 {
		return s
	}
	if a == Right {
		return strings.Repeat(" ", pad) + s
	}
	return s + strings.Repeat(" ", pad)
}

// truncate shortens s to at most width columns, marking any cut with a trailing
// ellipsis.
//
// A terminal cannot draw half of a wide character, so a cut landing inside one
// backs up to before it. That leaves the result a column short of the limit
// rather than a column over it — the direction that keeps the line from
// wrapping.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(s) <= width {
		return s
	}
	return runewidth.Truncate(s, width, ellipsis)
}

// headColumns returns the leading part of s that fits in width columns, with
// nothing to mark the cut — callers that want a mark add their own.
func headColumns(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return runewidth.Truncate(s, width, "")
}

// tailColumns returns the trailing part of s that fits in width columns.
func tailColumns(s string, width int) string {
	runes := []rune(s)
	used, i := 0, len(runes)
	for i > 0 {
		w := runewidth.RuneWidth(runes[i-1])
		if used+w > width {
			break
		}
		used += w
		i--
	}
	return string(runes[i:])
}
