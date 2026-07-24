package main

import (
	"fmt"
	"os"

	"github.com/xunull/pcpm/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "pcpm:", err)
		os.Exit(1)
	}
}
