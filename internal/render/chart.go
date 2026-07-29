package render

import (
	"fmt"
	"math"
	"slices"
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
	// Severity maps a value onto the colour gradient, from 0 to 1. Nil draws
	// in a single colour, for a quantity with no absolute meaning to convey.
	Severity func(float64) float64
	// Palette is how much colour the terminal can show.
	Palette Palette
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

	// The plot is narrower than the chart by the width of the axis labels, and
	// the columns have to be laid out for the plot: sized to the chart, the
	// oldest of them fall off the left while the time axis still claims to
	// start where they were.
	top := observedTop(points, series)
	if o.Max > 0 {
		top = o.Max
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
	columns := chartColumns(points, series, plotWidth, o.From, o.To)

	var b strings.Builder
	if o.Title != "" {
		fmt.Fprintf(&b, "%s\n", fit(o.Title, o.Width))
	}

	body := strings.Split(strings.TrimSuffix(
		Area(columns, AreaOptions{
			Width: plotWidth, Height: o.Height, Max: top,
			Palette: o.Palette, Severity: o.Severity,
		}), "\n"), "\n")
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

// TrafficSeries draws Traffic as bytes per second.
//
// A Point carries bytes *moved during its bucket*, so turning that into a rate
// needs the bucket's width — which is why this is built per query rather than
// being a plain accessor like the others. Sent and received are added: the
// question a chart answers is whether the line is busy, and splitting it into
// two areas stacked on one another answers it worse.
func TrafficSeries(bucket time.Duration) SeriesAccessor {
	return func(p watch.Point) (float64, float64) {
		if bucket <= 0 {
			return 0, 0
		}
		perSecond := float64(p.Traffic.InBytes+p.Traffic.OutBytes) / bucket.Seconds()
		return perSecond, perSecond
	}
}

// RSSSeries draws resident memory.
func RSSSeries(p watch.Point) (float64, float64) {
	return float64(p.RSSBytes), float64(p.PeakRSSBytes)
}

// chartColumns fits a series onto exactly as many slots as the terminal can
// draw, which is the most it can ever show. The data is adapted to that
// density in whichever direction it needs — there is no regime to pick between:
//
//   - a slot holding samples averages them, and carries the highest peak among
//     them, so the fill answers "how loaded" and the cap answers "did anything
//     happen";
//   - a slot holding none is interpolated between its neighbours, because two
//     consecutive samples say what the value did between them;
//   - unless those neighbours sit either side of a gap, where nothing was
//     collected and any line drawn would be invented.
//
// Averaging rather than taking the maximum is what stops the chart changing its
// story when the terminal is resized: both mean and max survive being combined
// again, a maximum used as the fill does not — narrowing a terminal would make
// an idle process look busy.
func chartColumns(points []watch.Point, series SeriesAccessor, width int, from, to time.Time) []Column {
	slots := max(width*2, 2)
	span := to.Sub(from)
	if span <= 0 || len(points) == 0 {
		return nil
	}

	type bucket struct {
		sum   float64
		n     int
		peak  float64
		known bool
	}
	acc := make([]bucket, slots)
	for _, p := range points {
		offset := p.At.Sub(from)
		if offset < 0 || offset >= span {
			continue
		}
		i := min(int(float64(offset)/float64(span)*float64(slots)), slots-1)
		value, peak := series(p)
		acc[i].sum += value
		acc[i].n++
		acc[i].peak = math.Max(acc[i].peak, peak)
		acc[i].known = true
	}

	columns := make([]Column, slots)
	for i := range columns {
		if acc[i].n == 0 {
			continue
		}
		columns[i] = Column{
			Value:   acc[i].sum / float64(acc[i].n),
			Peak:    acc[i].peak,
			Present: true,
		}
	}
	interpolate(columns, slotsPerGap(points, span, slots))
	return columns
}

// slotsPerGap is how many empty slots may be bridged before the emptiness means
// the collector stopped rather than merely that it samples less often than the
// terminal can draw. It comes from the data's own cadence, because the
// collection interval is a configuration key and assuming the default would
// misjudge every other setting.
func slotsPerGap(points []watch.Point, span time.Duration, slots int) int {
	cadence := pointSpacing(points)
	if cadence <= 0 {
		return 0
	}
	slotSpan := span / time.Duration(slots)
	if slotSpan <= 0 {
		return 0
	}
	// Two cadences of slack, matching what the query layer treats as a gap.
	return int(2 * cadence / slotSpan)
}

// interpolate fills runs of empty slots between two known ones, provided the
// run is short enough to be the space between samples rather than a period
// nothing was collected in.
func interpolate(columns []Column, maxRun int) {
	if maxRun < 1 {
		return
	}
	for i := 0; i < len(columns); i++ {
		if columns[i].Present {
			continue
		}
		start := i
		for i < len(columns) && !columns[i].Present {
			i++
		}
		// A run touching either end has only one neighbour, so there is nothing
		// to interpolate between.
		if start == 0 || i >= len(columns) {
			continue
		}
		run := i - start
		if run > maxRun {
			continue // the collector was not running; leave it blank
		}
		left, right := columns[start-1], columns[i]
		for j := range run {
			t := float64(j+1) / float64(run+1)
			// The peak is interpolated too, not carried across as the higher
			// of the two ends: an interpolated slot is a guess about the value,
			// and claiming it also reached the neighbour's peak would invent a
			// burst that nothing observed.
			columns[start+j] = Column{
				Value:   left.Value + (right.Value-left.Value)*t,
				Peak:    left.Peak + (right.Peak-left.Peak)*t,
				Present: true,
			}
		}
	}
}

// pointSpacing is how far apart the points are, taken from the data rather than
// from configuration: the collector's interval is a setting, and a chart that
// assumed the default would misjudge every other one. The median ignores the
// gaps, which are exactly the intervals that should stay uncovered.
func pointSpacing(points []watch.Point) time.Duration {
	if len(points) < 2 {
		return 0
	}
	steps := make([]time.Duration, 0, len(points)-1)
	for i := 1; i < len(points); i++ {
		if d := points[i].At.Sub(points[i-1].At); d > 0 {
			steps = append(steps, d)
		}
	}
	if len(steps) == 0 {
		return 0
	}
	slices.Sort(steps)
	return steps[len(steps)/2]
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

// CPUSeverity places a CPU figure on the colour gradient: half a core is the
// midpoint, a full core the top. Exported so a chart can say what its colours
// mean without the renderer having to know it is looking at CPU.
func CPUSeverity(cpuPercent float64) float64 { return severity(cpuPercent) }

// observedTop is the largest value the window reached, so the axis can be
// scaled before the columns are laid out — the layout needs the plot width,
// which needs the label width, which needs this.
func observedTop(points []watch.Point, series SeriesAccessor) float64 {
	top := 0.0
	for _, p := range points {
		value, peak := series(p)
		top = math.Max(top, math.Max(value, peak))
	}
	return top
}
