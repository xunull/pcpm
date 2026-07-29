package render

import (
	"strings"
	"testing"

	"github.com/xunull/pcpm/internal/top"
)

func TestFocusSummaryStatesWhatTheKeptRowsComeTo(t *testing.T) {
	matched := top.Sum{Count: 3, CPUPercent: 118, RSSBytes: 4 << 30}
	all := top.Sum{Count: 214, CPUPercent: 264, RSSBytes: 32 << 30}

	got := FocusSummary(matched, all)

	for _, want := range []string{"3", "214", "118%", "264%", Bytes(4 << 30), Bytes(32 << 30)} {
		if !strings.Contains(got, want) {
			t.Errorf("FocusSummary does not state %q:\n%s", want, got)
		}
	}
}

// The header's own figures are per core and rendered the same way, and a reader
// comparing the two lines should not have to notice a change of scale.
func TestFocusSummaryUsesTheSamePercentScaleAsTheHeader(t *testing.T) {
	line := FocusSummary(top.Sum{Count: 1, CPUPercent: 118.4}, top.Sum{Count: 2, CPUPercent: 264.9})
	header := TopHeader(top.Totals{BusyPercent: 118.4, AttributedPercent: 264.9})

	for _, want := range []string{"118%", "265%"} {
		if !strings.Contains(line, want) {
			t.Errorf("the summary renders a percentage differently from the header (%q missing):\n%s\n%s", want, line, header)
		}
	}
}

func TestFocusSummaryIsOneLine(t *testing.T) {
	got := FocusSummary(top.Sum{Count: 1}, top.Sum{Count: 9})
	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Errorf("want exactly one terminated line, got %q", got)
	}
}
