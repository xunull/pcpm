package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/xunull/pcpm/internal/listen"
	"github.com/xunull/pcpm/internal/render"
)

var portsCmd = &cobra.Command{
	Use:     "ports",
	Aliases: []string{"listen"},
	Short:   "List your processes that are listening on TCP ports",
	Long: "List the processes you own that hold a listening TCP socket, one row per process, " +
		"so you can spot something you forgot was running (a stray dev server) by the port it " +
		"occupies. A port marked * is bound to all interfaces (reachable off this machine). " +
		"Read-only: pcpm lists them; kill what you don't want yourself.",
	Args: cobra.NoArgs,
	RunE: runPorts,
}

func init() {
	portsCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	rootCmd.AddCommand(portsCmd)
}

func runPorts(cmd *cobra.Command, _ []string) error {
	outputFlag, _ := cmd.Flags().GetString("output")
	format, err := render.ParseFormat(outputFlag)
	if err != nil {
		return err
	}

	listeners, err := listen.Collect()
	if err != nil {
		return fmt.Errorf("collecting listeners: %w", err)
	}

	out := cmd.OutOrStdout()
	switch format {
	case render.FormatJSON:
		body, err := render.ListenersJSON(listeners)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		fmt.Fprint(out, render.ListenersTable(listeners, time.Now(), terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}
	return nil
}
