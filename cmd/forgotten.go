package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/xunull/pcpm/internal/config"
	"github.com/xunull/pcpm/internal/forgotten"
	"github.com/xunull/pcpm/internal/listen"
	"github.com/xunull/pcpm/internal/proc"
	"github.com/xunull/pcpm/internal/render"
)

// ErrForgottenFound is returned by `forgotten --fail-on-found` when at least one
// forgotten process tree was listed. It carries no user-facing message: the exit
// status is the signal and the listing is already on stdout (see main).
var ErrForgottenFound = errors.New("forgotten processes found")

var forgottenCmd = &cobra.Command{
	Use:     "forgotten",
	Aliases: []string{"forgot"},
	Short:   "List processes whose launching job is gone",
	Long: "List forgotten processes: the surviving roots of jobs nobody cleaned up. A process " +
		"is forgotten when its process group leader is dead — the job that started it is gone — " +
		"and its parent is not in that group, so it is the root of what was left behind. " +
		"Daemons that detach properly lead their own process group and are never listed. " +
		"Read-only: pcpm reports them; kill what you don't want yourself.",
	Args: cobra.NoArgs,
	RunE: runForgotten,
}

func init() {
	forgottenCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	forgottenCmd.Flags().StringArray("ignore", nil,
		"glob (matched against process name) to ignore; repeatable, adds to config")
	forgottenCmd.Flags().Bool("fail-on-found", false, "exit non-zero if anything is found")
	rootCmd.AddCommand(forgottenCmd)
}

func runForgotten(cmd *cobra.Command, _ []string) error {
	outputFlag, _ := cmd.Flags().GetString("output")
	format, err := render.ParseFormat(outputFlag)
	if err != nil {
		return err
	}

	cfg, err := config.Load(cmd.Flags(), configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	procs, err := proc.Collect()
	if err != nil {
		return fmt.Errorf("collecting processes: %w", err)
	}

	trees := forgotten.Detect(procs, listeningPorts())
	trees, err = forgotten.ApplyIgnore(trees, cfg.Ignore)
	if err != nil {
		return fmt.Errorf("applying ignore list: %w", err)
	}

	out := cmd.OutOrStdout()
	switch format {
	case render.FormatJSON:
		body, err := render.ForgottenJSON(trees)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		home, _ := os.UserHomeDir()
		fmt.Fprint(out, render.ForgottenTable(trees, time.Now(), home, terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}

	if failOnFound, _ := cmd.Flags().GetBool("fail-on-found"); failOnFound && len(trees) > 0 {
		return ErrForgottenFound
	}
	return nil
}

// listeningPorts maps pid to the TCP ports it listens on, so a forgotten root
// can report the ports its whole tree holds. Port data is a convenience, not the
// finding itself, so a failure here degrades to "no ports known" rather than
// failing the command.
func listeningPorts() map[int32][]listen.Port {
	listeners, err := listen.Collect()
	if err != nil {
		return nil
	}
	byPID := make(map[int32][]listen.Port, len(listeners))
	for _, l := range listeners {
		byPID[l.PID] = l.Ports
	}
	return byPID
}
