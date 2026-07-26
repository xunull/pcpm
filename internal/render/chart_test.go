package render

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/xunull/pcpm/internal/watch"
)

func chartWindow() (time.Time, time.Time) {
	from := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	return from, from.Add(time.Hour)
}

func TestChartDrawsTheSeriesWithATimeAxis(t *testing.T) {
	from, to := chartWindow()
	var points []watch.Point
	for i := range 30 {
		points = append(points, watch.Point{
			At:         from.Add(time.Duration(i) * 2 * time.Minute),
			CPUPercent: 20 + float64(i%5)*10,
		})
	}

	out := Chart(points, CPUValue, ChartOptions{
		Width: 60, Height: 8, From: from, To: to,
		Caption: "CPU", Label: Percent,
	})

	if !strings.Contains(out, "CPU") {
		t.Errorf("caption is missing:\n%s", out)
	}
	// the window's start and end should be readable off the x axis
	if !strings.Contains(out, from.Local().Format("15:04")) {
		t.Errorf("x axis does not show the window start %s:\n%s", from.Local().Format("15:04"), out)
	}
	if !strings.Contains(out, "%") {
		t.Errorf("y axis is not labelled as a percentage:\n%s", out)
	}
}

// A period with no data must not be drawn through: a straight line across it
// claims the value held steady, when nothing at all is known about it.
func TestChartBreaksTheLineAcrossMissingData(t *testing.T) {
	from, to := chartWindow()
	var points []watch.Point
	for i := range 60 {
		// nothing between minutes 20 and 40
		if i >= 20 && i < 40 {
			continue
		}
		points = append(points, watch.Point{
			At:         from.Add(time.Duration(i) * time.Minute),
			CPUPercent: 50,
		})
	}

	series := columnise(points, CPUValue, ChartOptions{Width: 60, From: from, To: to})

	middle := series[25:35]
	for i, v := range middle {
		if !math.IsNaN(v) {
			t.Errorf("column %d inside the hole has value %v; want NaN so the line breaks", 25+i, v)
		}
	}
	if math.IsNaN(series[0]) || math.IsNaN(series[59]) {
		t.Error("columns with data should not be blank")
	}
}

// A bucket that spans a gap has a sound average of its own, but the shape
// leading into it is unknown — so the line breaks just before it.
func TestChartBreaksBeforeABucketThatSpansAGap(t *testing.T) {
	from, to := chartWindow()
	points := []watch.Point{
		{At: from.Add(10 * time.Minute), CPUPercent: 30},
		{At: from.Add(11 * time.Minute), CPUPercent: 40, Gap: true},
		{At: from.Add(12 * time.Minute), CPUPercent: 35},
	}

	series := columnise(points, CPUValue, ChartOptions{Width: 60, From: from, To: to})

	if !math.IsNaN(series[10]) {
		t.Errorf("the column before a gap bucket should break the line, got %v", series[10])
	}
	if math.IsNaN(series[11]) {
		t.Error("the gap bucket's own value is sound and should still be plotted")
	}
}

func TestChartWithNoPoints(t *testing.T) {
	from, to := chartWindow()

	out := Chart(nil, CPUValue, ChartOptions{Width: 40, Height: 6, From: from, To: to, Caption: "CPU"})

	if out == "" {
		t.Error("an empty series should still render something rather than nothing")
	}
	if !strings.Contains(out, "CPU") {
		t.Errorf("the caption should survive an empty series:\n%s", out)
	}
}

// Memory and CPU read very differently, so the axis formatter is the caller's.
func TestChartLabelsTheAxisWithTheCallersFormatter(t *testing.T) {
	from, to := chartWindow()
	points := []watch.Point{
		{At: from.Add(time.Minute), RSSBytes: 300 << 20},
		{At: from.Add(2 * time.Minute), RSSBytes: 400 << 20},
	}

	out := Chart(points, RSSValue, ChartOptions{
		Width: 40, Height: 6, From: from, To: to,
		Caption: "memory", Label: func(v float64) string { return Bytes(int64(v)) },
	})

	if !strings.Contains(out, "MB") {
		t.Errorf("y axis should be labelled in bytes:\n%s", out)
	}
}

// A point outside the window is not the window's business.
func TestColumniseIgnoresPointsOutsideTheWindow(t *testing.T) {
	from, to := chartWindow()
	points := []watch.Point{
		{At: from.Add(-time.Hour), CPUPercent: 99},
		{At: to.Add(time.Hour), CPUPercent: 99},
		{At: from.Add(30 * time.Minute), CPUPercent: 42},
	}

	series := columnise(points, CPUValue, ChartOptions{Width: 60, From: from, To: to})

	plotted := 0
	for _, v := range series {
		if !math.IsNaN(v) {
			plotted++
			if v != 42 {
				t.Errorf("plotted a value from outside the window: %v", v)
			}
		}
	}
	if plotted != 1 {
		t.Errorf("want exactly the one in-window point plotted, got %d", plotted)
	}
}
