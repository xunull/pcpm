package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/xunull/pcpm/cmd"
)

func main() {
	err := cmd.Execute()
	if err == nil {
		return
	}
	// --fail-on-found signals via exit status only; its listing is already on
	// stdout, so don't print a redundant error line for it.
	if !errors.Is(err, cmd.ErrCandidatesFound) {
		fmt.Fprintln(os.Stderr, "pcpm:", err)
	}
	os.Exit(1)
}
