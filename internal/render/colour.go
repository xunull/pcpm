package render

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// Depth is how much colour a terminal can show.
type Depth int

const (
	DepthNone Depth = iota // no colour at all
	Depth16                // the original ANSI set
	Depth256               // xterm's 256-colour cube
	DepthTrue              // 24-bit
)

// Palette turns a position in a severity range into a colour escape.
//
// It adapts to what the terminal can *display*, never to what the terminal
// looks like: there is no way to ask a terminal for its theme, and guessing is
// how the first version of these charts ended up drawing its axis in a grey
// that vanished into the background. Structural elements therefore carry no
// colour at all, and the background is never painted.
type Palette struct {
	Depth Depth
}

// gradientStops are btop's CPU gradient (src/btop_theme.cpp, v1.4.7). They are
// mid-tones rather than saturated primaries, which is what lets one palette
// stay legible on a light terminal and a dark one alike.
var gradientStops = [3][3]int{
	{0x77, 0xca, 0x9b}, // green
	{0xcb, 0xc0, 0x6c}, // yellow
	{0xdc, 0x4c, 0x4c}, // red
}

// DetectPalette reads the environment for how much colour to use.
func DetectPalette() Palette {
	return Palette{Depth: detectDepth(os.LookupEnv)}
}

// detectDepth decides from the environment, taking a lookup function so the
// decision can be tested without mutating the process's own.
//
// btop takes this from configuration instead. pcpm is run occasionally rather
// than left open, so requiring a config file before the colours are right is
// not reasonable; where nothing is declared it settles on 256, which every
// terminal worth colouring for can show.
func detectDepth(lookup func(string) (string, bool)) Depth {
	// NO_COLOR is honoured whatever its value, including empty.
	if _, ok := lookup("NO_COLOR"); ok {
		return DepthNone
	}
	// A terminal saying it cannot colour is the one veto that outranks a
	// capability declaration; an absent TERM is merely uninformative.
	term, _ := lookup("TERM")
	if term == "dumb" {
		return DepthNone
	}
	if ct, ok := lookup("COLORTERM"); ok {
		switch strings.ToLower(ct) {
		case "truecolor", "24bit":
			return DepthTrue
		}
	}
	if term == "" {
		return DepthNone
	}
	// A kernel console has the original sixteen and nothing more.
	if term == "linux" || term == "console" {
		return Depth16
	}
	return Depth256
}

// Gradient returns the escape for a position from 0 (calm) to 1 (severe).
func (p Palette) Gradient(at float64) string {
	if p.Depth == DepthNone {
		return ""
	}
	at = math.Min(math.Max(at, 0), 1)
	r, g, b := interpolateStops(at)

	switch p.Depth {
	case DepthTrue:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
	case Depth256:
		return fmt.Sprintf("\x1b[38;5;%dm", trueColorTo256(r, g, b))
	default:
		// The original sixteen, as btop's tty theme does it.
		switch {
		case at > 0.66:
			return "\x1b[91m" // bright red
		case at > 0.33:
			return "\x1b[93m" // bright yellow
		default:
			return "\x1b[92m" // bright green
		}
	}
}

// Reset returns the terminal to its own colours.
func (p Palette) Reset() string {
	if p.Depth == DepthNone {
		return ""
	}
	return "\x1b[0m"
}

// interpolateStops walks green → yellow → red in two halves, the way btop
// builds its 101-step gradient.
func interpolateStops(at float64) (int, int, int) {
	from, to, t := 0, 1, at*2
	if at > 0.5 {
		from, to, t = 1, 2, (at-0.5)*2
	}
	mix := func(i int) int {
		a, b := gradientStops[from][i], gradientStops[to][i]
		return a + int(math.Round(float64(b-a)*t))
	}
	return mix(0), mix(1), mix(2)
}

// trueColorTo256 maps a 24-bit colour onto xterm's cube, using btop's formula
// (src/btop_theme.cpp): the greyscale ramp when the channels agree, the 6×6×6
// cube otherwise.
func trueColorTo256(r, g, b int) int {
	if red := int(math.Round(float64(r) / 11)); red == int(math.Round(float64(g)/11)) && red == int(math.Round(float64(b)/11)) {
		return 232 + red
	}
	return int(math.Round(float64(r)/51))*36 + int(math.Round(float64(g)/51))*6 + int(math.Round(float64(b)/51)) + 16
}

// severity places a value on the gradient by what it means, not by where it
// sits on the chart.
//
// A chart's axis is fitted to its own data, so height says nothing absolute:
// colouring by row would paint a process using 8% of a core in the same red as
// one saturating four. Half a core is the midpoint and a full core the top,
// which are quantities rather than positions.
func severity(cpuPercent float64) float64 {
	const fullCore = 100.0
	return math.Min(math.Max(cpuPercent/fullCore, 0), 1)
}
