package render

import (
	"strings"
	"testing"
)

// A character's width on screen is a third number, agreeing with neither of the
// other two. Everything that lays out a table has to use this one.
func TestDisplayWidthIsNeitherBytesNorRunes(t *testing.T) {
	const s = "汽水音乐"

	if got := len(s); got != 12 {
		t.Errorf("bytes = %d, want 12", got)
	}
	if got := len([]rune(s)); got != 4 {
		t.Errorf("runes = %d, want 4", got)
	}
	if got := displayWidth(s); got != 8 {
		t.Errorf("displayWidth = %d, want 8", got)
	}
}

func TestDisplayWidthCountsEmojiAsWide(t *testing.T) {
	if got := displayWidth("🔥"); got != 2 {
		t.Errorf("displayWidth(🔥) = %d, want 2", got)
	}
}

// A cell padded by byte count leaves the next column short by the difference
// between the string's bytes and its columns — which for CJK is a third of it.
func TestPadToFillsToDisplayWidth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cell  string
		width int
		align Align
		want  string
	}{
		{"ascii left", "bash", 8, Left, "bash    "},
		{"ascii right", "12.8", 8, Right, "    12.8"},
		{"wide left", "汽水音乐", 10, Left, "汽水音乐  "},
		{"wide right", "汽水音乐", 10, Right, "  汽水音乐"},
		{"already wider", "abcdef", 3, Left, "abcdef"},
		{"exact fit", "abc", 3, Right, "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := padTo(tc.cell, tc.width, tc.align)
			if got != tc.want {
				t.Errorf("padTo(%q, %d) = %q, want %q", tc.cell, tc.width, got, tc.want)
			}
		})
	}
}

// A terminal cannot render half of a wide character, so a cut that lands inside
// one must fall back to before it, leaving the column one short rather than
// spilling one over.
func TestTruncateNeverSplitsAWideCharacter(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9} {
		got := truncate("汽水音乐 Helper", width)
		if w := displayWidth(got); w > width {
			t.Errorf("truncate to %d gave %q, which is %d columns wide", width, got, w)
		}
	}
}

func TestTruncateLeavesWhatAlreadyFits(t *testing.T) {
	if got := truncate("bash", 10); got != "bash" {
		t.Errorf("truncate = %q, want it untouched", got)
	}
	if got := truncate("汽水音乐", 8); got != "汽水音乐" {
		t.Errorf("truncate = %q, want it untouched at exactly its own width", got)
	}
}

func TestTruncateMarksWhereItCut(t *testing.T) {
	got := truncate(strings.Repeat("x", 40), 10)

	if displayWidth(got) > 10 {
		t.Errorf("%q is wider than 10", got)
	}
	if !strings.HasSuffix(got, ellipsis) {
		t.Errorf("truncate = %q, want it to end with an ellipsis", got)
	}
}
