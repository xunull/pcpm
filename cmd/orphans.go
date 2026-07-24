package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xunull/pcpm/internal/config"
	"github.com/xunull/pcpm/internal/orphan"
	"github.com/xunull/pcpm/internal/render"
)

// ErrCandidatesFound is returned by `orphans --fail-on-found` when at least one
// candidate was listed. It carries no user-facing message: the exit status is
// the signal and the listing is already on stdout (see main).
var ErrCandidatesFound = errors.New("orphaned application process candidates found")

var orphansCmd = &cobra.Command{
	Use:     "orphans",
	Aliases: []string{"orphan"},
	Short:   "List orphaned application process candidates",
	Long: "List processes reparented to init (PPID 1) that belong to a real login " +
		"user — candidate orphaned application processes, such as a dev server still " +
		"running after the shell or tool that launched it died. Read-only: pcpm only " +
		"surfaces candidates for you to judge and never touches a process.",
	Args: cobra.NoArgs,
	RunE: runOrphans,
}

func init() {
	orphansCmd.Flags().Int32("min-uid", orphan.DefaultMinUID(runtime.GOOS),
		"minimum uid treated as a real login user")
	orphansCmd.Flags().StringArray("ignore", nil,
		"glob (matched against process name) to ignore; repeatable, adds to config")
	orphansCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	orphansCmd.Flags().Bool("fail-on-found", false, "exit non-zero if any candidate is found")
}

func runOrphans(cmd *cobra.Command, _ []string) error {
	// Validate the output format before the (comparatively expensive) scan.
	outputFlag, _ := cmd.Flags().GetString("output")
	format, err := render.ParseFormat(outputFlag)
	if err != nil {
		return err
	}

	cfg, err := config.Load(cmd.Flags(), configPath, runtime.GOOS)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	procs, err := orphan.Collect()
	if err != nil {
		return fmt.Errorf("collecting processes: %w", err)
	}

	candidates := orphan.Candidates(procs, cfg.MinUID)
	candidates, err = orphan.ApplyIgnore(candidates, cfg.Ignore)
	if err != nil {
		return fmt.Errorf("applying ignore list: %w", err)
	}

	out := cmd.OutOrStdout()
	switch format {
	case render.FormatJSON:
		body, err := render.JSON(candidates)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		fmt.Fprint(out, render.Table(candidates, time.Now(), terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}

	if failOnFound, _ := cmd.Flags().GetBool("fail-on-found"); failOnFound && len(candidates) > 0 {
		return ErrCandidatesFound
	}
	return nil
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
