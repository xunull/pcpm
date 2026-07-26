package render

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/xunull/pcpm/internal/watch"
)

// ChartOptions is how a series should be drawn.
type ChartOptions struct {
	Width, Height int
	From, To      time.Time
	// Title says what the chart is. It goes above the plot, in the terminal's
	// own colours: the first version put it underneath in a hardcoded grey and
	// it was invisible, which left no way to tell CPU from memory.
	Title string
	// Label formats a value for the y axis. Percentages and byte counts read
	// very differently, so the caller supplies it.
	Label func(float64) string
	// Max is the value at the top of the chart. Zero fits the data.
	Max float64
	// Flat draws without the severity gradient, for a quantity whose scale is
	// fitted to its own data and so cannot say what "high" means.
	Flat bool
	// Ceiling rounds the fitted top up to a value that means something, so that
	// a row's height on the chart still corresponds to a real quantity.
	Ceiling func(float64) float64
}

// Chart draws a target's history as a filled area with axes and a title.
//
// The fill is the average over each slice of time and the cap above it is that
// slice's peak, because a compressed bucket has to answer both "how loaded was
// it" and "did anything happen at all" (ADR-0010).
func Chart(points []watch.Point, series SeriesAccessor, o ChartOptions) string {
	if o.Width < 12 {
		o.Width = 12
	}
	if o.Height < 3 {
		o.Height = 3
	}

	columns, top := chartColumns(points, series, o)
	if o.Max > 0 {
		top = o.Max
	} else if o.Ceiling != nil {
		// Rounding up to a meaningful boundary is itself the headroom; adding
		// more on top would push a 92% peak onto a two-core axis and waste half
		// the chart.
		top = o.Ceiling(top)
	} else {
		// Headroom above the observed peak. Scaling exactly to the maximum
		// makes a steady value fill the chart, which reads as "at its limit"
		// when it is merely constant.
		top *= 1.25
	}
	if top <= 0 {
		top = 1
	}

	label := o.Label
	if label == nil {
		label = func(v float64) string { return fmt.Sprintf("%.0f", v) }
	}
	// The gutter fits the widest label, not just the top one: Percent renders
	// 0 as "0.0%" and 70 as "70%", so sizing to the top overflows the row.
	gutter := max(len([]rune(label(top))), len([]rune(label(0)))) + 1
	plotWidth := max(o.Width-gutter-1, 8)

	var b strings.Builder
	if o.Title != "" {
		fmt.Fprintf(&b, "%s\n", fit(o.Title, o.Width))
	}

	body := strings.Split(strings.TrimSuffix(
		Area(columns, AreaOptions{Width: plotWidth, Height: o.Height, Max: top, Flat: o.Flat}), "\n"), "\n")
	for i, row := range body {
		switch i {
		case 0:
			fmt.Fprintf(&b, "%*s │%s\n", gutter-1, label(top), row)
		case len(body) - 1:
			fmt.Fprintf(&b, "%*s │%s\n", gutter-1, label(0), row)
		default:
			fmt.Fprintf(&b, "%*s │%s\n", gutter-1, "", row)
		}
	}
	fmt.Fprintf(&b, "%*s └%s\n", gutter-1, "", strings.Repeat("─", plotWidth))
	fmt.Fprintf(&b, "%*s %s\n", gutter-1, "", timeAxis(o.From, o.To, plotWidth))
	return b.String()
}

// SeriesAccessor reads the value and the peak a chart should draw for a point.
type SeriesAccessor func(watch.Point) (value, peak float64)

// CPUSeries draws CPU: the bucket's rate, capped at its peak.
func CPUSeries(p watch.Point) (float64, float64) { return p.CPUPercent, p.PeakCPUPercent }

// RSSSeries draws resident memory.
func RSSSeries(p watch.Point) (float64, float64) {
	return float64(p.RSSBytes), float64(p.PeakRSSBytes)
}

// chartColumns lays points along the window, one slot per half-character, and
// reports the largest value seen so the axis can be scaled to it.
//
// Slots with no point stay absent rather than being filled by their neighbours:
// nothing was collected then, and a chart that guessed would let a stopped
// collector pass for a quiet process.
func chartColumns(points []watch.Point, series SeriesAccessor, o ChartOptions) ([]Column, float64) {
	slots := max(o.Width*2, 2)
	span := o.To.Sub(o.From)
	if span <= 0 || len(points) == 0 {
		return nil, 0
	}

	columns := make([]Column, slots)
	top := 0.0
	for _, p := range points {
		offset := p.At.Sub(o.From)
		if offset < 0 || offset >= span {
			continue
		}
		i := min(int(float64(offset)/float64(span)*float64(slots)), slots-1)
		value, peak := series(p)
		// Several points can share a slot when the window is long. Keep the
		// heavier: a slot that held a burst should look like it.
		if !columns[i].Present || value > columns[i].Value {
			columns[i].Value = value
		}
		columns[i].Peak = math.Max(columns[i].Peak, peak)
		columns[i].Present = true
		top = math.Max(top, math.Max(value, peak))
	}
	return columns, top
}

// timeAxis labels the window's span beneath the plot.
func timeAxis(from, to time.Time, width int) string {
	if width < 12 {
		return ""
	}
	left := from.Format("15:04")
	right := to.Format("15:04")
	middle := from.Add(to.Sub(from) / 2).Format("15:04")
	pad := width - len(left) - len(middle) - len(right)
	if pad < 2 {
		return left + strings.Repeat(" ", max(width-len(left)-len(right), 1)) + right
	}
	return left + strings.Repeat(" ", pad/2) + middle + strings.Repeat(" ", pad-pad/2) + right
}

// WholeCores rounds a CPU ceiling up to the next whole core, so that the top of
// the chart is always 100%, 200%, and so on. Keeping the axis on a real
// boundary is what lets the colour gradient mean anything: half way up is half
// a core, not half of whatever this window happened to reach.
func WholeCores(top float64) float64 {
	cores := math.Ceil(top / 100)
	if cores < 1 {
		cores = 1
	}
	return cores * 100
}
