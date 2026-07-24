package cmd

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xunull/pcpm/internal/orphan"
	"github.com/xunull/pcpm/internal/render"
)

var minUID int32

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
	orphansCmd.Flags().Int32Var(&minUID, "min-uid", 0,
		"minimum uid treated as a real login user (default: 1000 on Linux, 500 on macOS)")
}

func runOrphans(cmd *cobra.Command, _ []string) error {
	procs, err := orphan.Collect()
	if err != nil {
		return fmt.Errorf("collecting processes: %w", err)
	}

	mu := minUID
	if !cmd.Flags().Changed("min-uid") {
		mu = orphan.DefaultMinUID(runtime.GOOS)
	}

	candidates := orphan.Candidates(procs, mu)

	out := cmd.OutOrStdout()
	fmt.Fprint(out, render.Table(candidates, time.Now(), terminalWidth(out)))
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
