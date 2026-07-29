//go:build darwin

package watch

import (
	"fmt"
	"os/exec"
)

// newTrafficCommand builds the command that streams per-process byte counters.
//
// macOS is the only platform where this can be read without privilege, because
// Apple exposes a kernel statistics channel that nettop reads. The flags matter:
//
//	-P  per-process totals rather than a row per connection (roughly a sixth
//	    the output, and connections are not what a Watch Target is about)
//	-x  raw byte counts rather than human-readable suffixes
//	-L  CSV, which is the only form safe to parse — process names contain
//	    spaces, so the column-aligned default cannot be split reliably
//	-s  how often to report
//
// -L takes a sample count, and 0 means keep going. That is the whole reason
// this is a long-lived child rather than a call per tick: a freshly started
// nettop does not see connections that already existed, which for a watched
// server is all of them. Measured, one-per-tick recovered 0–29% of actual
// traffic; this form is monotonic and within 5–10% (ADR-0012).
func newTrafficCommand(intervalSeconds int) *exec.Cmd {
	return exec.Command("nettop",
		"-P", "-x", "-L", "0",
		"-s", fmt.Sprint(intervalSeconds),
		"-J", "bytes_in,bytes_out",
	)
}
