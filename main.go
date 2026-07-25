package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/xunull/pcpm/cmd"
)

// Stamped at release time via ldflags, e.g.
// -X main.version=0.1.0 -X main.commit=abc1234 -X main.date=2026-07-25T06:00:00Z
// A plain `go build` leaves them empty and pcpm reports an unstamped build.
var (
	version string
	commit  string
	date    string
)

func main() {
	cmd.SetBuildInfo(version, commit, date)

	err := cmd.Execute()
	if err == nil {
		return
	}
	// --fail-on-found signals via exit status only; its listing is already on
	// stdout, so don't print a redundant error line for it.
	if !errors.Is(err, cmd.ErrForgottenFound) {
		fmt.Fprintln(os.Stderr, "pcpm:", err)
	}
	os.Exit(1)
}
