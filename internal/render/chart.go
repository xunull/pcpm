package render

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/guptarohit/asciigraph"

	"github.com/xunull/pcpm/internal/watch"
)

// ChartOptions is how a series should be drawn.
type ChartOptions struct {
	Width, Height int
	From, To      time.Time
	Caption       string
	// Label formats a y-axis value. Percentages and byte counts read very
	// differently, so the caller supplies it.
	Label func(float64) string
	// WarnAbove colours the line above this value. Zero disables it.
	WarnAbove float64
	// Overlay is a second series drawn against the first — one process's own
	// contribution against its tree's total. The command a person recognises is
	// usually a wrapper, so seeing the two apart is what answers "which part of
	// this is doing the work".
	Overlay []watch.Point
}

// Chart draws a target's history as a line over a fixed time window.
//
// Periods with no data break the line rather than being interpolated across:
// the machine was asleep or the collector was not running, and a straight line
// through that would claim the value held steady when nothing is known about
// it. A bucket flagged as spanning a gap breaks the line just before it for the
// same reason — its own average is sound, but the shape leading into it is not.
func Chart(points []watch.Point, value func(watch.Point) float64, o ChartOptions) string {
	if o.Width < 8 {
		o.Width = 8
	}
	if o.Height < 3 {
		o.Height = 3
	}
	if len(points) == 0 {
		return fmt.Sprintf("%s\n%s\n", o.Caption, strings.Repeat("─", o.Width))
	}

	series := columnise(points, value, o)
	plots := [][]float64{series}
	colors := []asciigraph.AnsiColor{asciigraph.Green}
	if len(o.Overlay) > 0 {
		plots = append(plots, columnise(o.Overlay, value, o))
		colors = append(colors, asciigraph.Blue)
	}

	opts := []asciigraph.Option{
		asciigraph.Width(o.Width),
		asciigraph.Height(o.Height),
		asciigraph.LowerBound(0),
		asciigraph.AxisColor(asciigraph.Gray),
		asciigraph.LabelColor(asciigraph.Gray),
		asciigraph.SeriesColors(colors...),
		asciigraph.XAxisRange(float64(o.From.Unix()), float64(o.To.Unix())),
		asciigraph.XAxisTickCount(6),
		asciigraph.XAxisValueFormatter(func(v float64) string {
			return time.Unix(int64(v), 0).Format("15:04")
		}),
	}
	if o.Label != nil {
		opts = append(opts, asciigraph.YAxisValueFormatter(o.Label))
	}
	// A busy target should read as busy at a glance, without comparing against
	// the axis. Threshold colouring applies to a single line only — with two,
	// colour is what tells them apart.
	if o.WarnAbove > 0 && len(plots) == 1 {
		opts = append(opts, asciigraph.ColorAbove(asciigraph.Red, o.WarnAbove))
	}
	if o.Caption != "" {
		opts = append(opts, asciigraph.Caption(o.Caption), asciigraph.CaptionColor(asciigraph.Gray))
	}
	if len(plots) == 1 {
		return asciigraph.Plot(series, opts...) + "\n"
	}
	return asciigraph.PlotMany(plots, opts...) + "\n"
}

// columnise lays points out along the window, one slot per column, leaving NaN
// wherever nothing is known. asciigraph breaks its line at NaN rather than
// drawing through it.
func columnise(points []watch.Point, value func(watch.Point) float64, o ChartOptions) []float64 {
	series := make([]float64, o.Width)
	for i := range series {
		series[i] = math.NaN()
	}

	span := o.To.Sub(o.From)
	if span <= 0 {
		return series
	}
	for _, p := range points {
		offset := p.At.Sub(o.From)
		if offset < 0 || offset >= span {
			continue
		}
		col := int(float64(offset) / float64(span) * float64(o.Width))
		if col >= o.Width {
			col = o.Width - 1
		}
		series[col] = value(p)
		// The bucket's own average is sound; what is unknown is the shape of
		// the period leading into it. Break the line there rather than let it
		// slope smoothly out of a hole.
		if p.Gap && col > 0 {
			series[col-1] = math.NaN()
		}
	}
	return series
}

// CPUValue reads a point's CPU percentage, for Chart.
func CPUValue(p watch.Point) float64 { return p.CPUPercent }

// RSSValue reads a point's resident memory, for Chart.
func RSSValue(p watch.Point) float64 { return float64(p.RSSBytes) }
