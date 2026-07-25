// Package cmd wires the pcpm cobra command tree.
package cmd

import (
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// configPath backs the --config persistent flag; empty means "search the
// default per-user config directory".
var configPath string

var rootCmd = &cobra.Command{
	Use:   "pcpm",
	Short: "Find processes nothing is looking after any more",
	Long: "pcpm finds forgotten processes — the surviving roots of jobs nobody cleaned up, " +
		"such as a dev server an AI coding agent or terminal started and never stopped — " +
		"and shows what is listening on your TCP ports.",
	SilenceUsage: true,
	// main owns error reporting (and the ErrForgottenFound signal), so cobra
	// should not also print errors.
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "",
		"config file (default: $XDG_CONFIG_HOME/pcpm/config.yaml)")
	rootCmd.AddCommand(forgottenCmd)
}

// Execute runs the root command and returns any error to main.
func Execute() error {
	return rootCmd.Execute()
}

// terminalWidth returns the column width of w when it is a terminal, or 0
// (meaning "do not truncate") when w is redirected, piped, or not a terminal.
func terminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(f.Fd()))
	if err != nil || width <= 0 {
		return 0
	}
	return width
}
