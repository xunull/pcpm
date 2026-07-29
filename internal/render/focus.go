package render

import (
	"fmt"

	"github.com/xunull/pcpm/internal/top"
)

// FocusSummary states what the rows a Focus keeps come to, against what the
// whole ranking came to.
//
// It exists because a Focus hides rows without changing a digit of the header
// above it. The header's job is to say how much of the machine the rows account
// for (ADR-0011); once some of those rows are hidden it is answering about a
// ranking the reader can no longer see, and nothing on screen would say so.
//
// Both figures cover every matching process, not the rows that fit on screen.
// Counting only what is drawn would make the number change when the window is
// resized, and report one thing in a tall terminal and another in a short one.
//
// CPU and memory are both given because the sort key can be switched at a
// keystroke, and a line that only spoke of CPU would become half a truth the
// moment it was.
//
// The memory denominator is the ranking's own resident total rather than the
// machine's used memory, which the header shows. Those two are not comparable:
// resident sizes count shared pages once per process, so adding them up
// overshoots what the machine is actually using — often by a lot. Writing
// "of 24.1G" against that sum would assert a part-of-whole relation that does
// not hold. Against the ranking's own total it does, and it matches how the CPU
// figures already read, where the header's attributed percentage is likewise the
// sum over every ranked row.
func FocusSummary(matched, all top.Sum) string {
	return fmt.Sprintf("matching %d of %d  ·  CPU %s%% of %s%%  ·  RSS %s of %s\n",
		matched.Count, all.Count,
		rankedPercent(matched.CPUPercent), rankedPercent(all.CPUPercent),
		Bytes(matched.RSSBytes), Bytes(all.RSSBytes))
}
