// Package cmd wires the pcpm cobra command tree.
package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "pcpm",
	Short: "Find processes that have been orphaned to init",
	Long: "pcpm finds orphaned application processes — dev/app processes " +
		"reparented to init (PPID 1) after the shell or tool that launched them died.",
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(orphansCmd)
}

// Execute runs the root command and returns any error to main.
func Execute() error {
	return rootCmd.Execute()
}
