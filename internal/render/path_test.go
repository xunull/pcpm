package render

import (
	"strings"
	"testing"
)

// deep is longer than any column pcpm gives a path, and the segment worth
// finding is buried in the middle of it.
const deep = "/Users/q/xunull-repository/xunull-github/open-source/pcpm"

func TestWithoutAMatchTheColumnIsExactlyWhatItWas(t *testing.T) {
	for _, path := range []string{deep, "/short", "", "/Users/q/src"} {
		if got, want := ShortPathAround(path, "/Users/q", 32, ""), ShortPath(path, "/Users/q", 32); got != want {
			t.Errorf("ShortPathAround(%q, no match) = %q; want %q", path, got, want)
		}
	}
}

// The whole point: a row kept by a word the column was not showing looked
// arbitrary, because the collapse always kept the tail.
func TestTheMatchedSegmentSurvivesTheCollapse(t *testing.T) {
	plain := ShortPath(deep, "", 32)
	if strings.Contains(plain, "xunull-repository") {
		t.Fatal("this path no longer needs collapsing; the test proves nothing")
	}

	got := ShortPathAround(deep, "", 32, "xunull-repository")

	if !strings.Contains(got, "xunull-repository") {
		t.Errorf("the matched segment is not visible: %q", got)
	}
	if displayWidth(got) > 32 {
		t.Errorf("%q is %d columns; the column allows 32", got, displayWidth(got))
	}
}

func TestAMatchIsFoundWhateverItsCase(t *testing.T) {
	got := ShortPathAround(deep, "", 32, "XUNULL-REPOSITORY")
	if !strings.Contains(got, "xunull-repository") {
		t.Errorf("an upper-case match was not located: %q", got)
	}
}

// A path already shown whole has nothing to collapse around.
func TestAPathThatFitsIsUntouched(t *testing.T) {
	if got := ShortPathAround("/src/pcpm", "", 32, "src"); got != "/src/pcpm" {
		t.Errorf("a path that fits was rewritten: %q", got)
	}
}

// The tail is what the collapse already keeps, so re-centring on it would
// change the column for no gain.
func TestAMatchAlreadyInTheTailChangesNothing(t *testing.T) {
	if got, want := ShortPathAround(deep, "", 32, "pcpm"), ShortPath(deep, "", 32); got != want {
		t.Errorf("ShortPathAround = %q; want the ordinary collapse %q", got, want)
	}
}

// A term can span a separator, and then no single segment holds it. Falling
// back is honest: the column cannot show a reason it cannot locate.
func TestAMatchSpanningSegmentsFallsBack(t *testing.T) {
	if got, want := ShortPathAround(deep, "", 32, "repository/xunull-github"), ShortPath(deep, "", 32); got != want {
		t.Errorf("ShortPathAround = %q; want the ordinary collapse %q", got, want)
	}
}

// A matched segment too wide to sit beside the tail keeps the column; showing
// why the row is there matters more than showing where it ends.
func TestAWideMatchedSegmentKeepsTheColumn(t *testing.T) {
	path := "/a/" + strings.Repeat("z", 40) + "/b/c/d"

	got := ShortPathAround(path, "", 32, "zzz")

	if displayWidth(got) > 32 {
		t.Errorf("%q is %d columns; the column allows 32", got, displayWidth(got))
	}
	if !strings.Contains(got, "zzz") {
		t.Errorf("the matched segment vanished entirely: %q", got)
	}
}

func TestTheColumnCarriesNoEscapeSequences(t *testing.T) {
	got := ShortPathAround(deep, "", 32, "xunull-repository")
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("an escape sequence reached a table cell: %q", got)
	}
}
