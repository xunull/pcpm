package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/xunull/pcpm/internal/config"
	"github.com/xunull/pcpm/internal/render"
	"github.com/xunull/pcpm/internal/top"
	"github.com/xunull/pcpm/internal/tui"
)

var topCmd = &cobra.Command{
	Use:   "top",
	Short: "Rank the processes consuming CPU right now",
	Long: "Rank the processes using the most CPU at this moment. The kernel keeps no such " +
		"figure — only a counter of CPU seconds consumed since each process started — so pcpm " +
		"reads every process twice, an interval apart, and reports the difference. That is why " +
		"the command takes a moment to answer.\n\n" +
		"Percentages are per core: 100% is one core fully occupied, and a process spread over " +
		"eight cores reads 800%.\n\n" +
		"Only your own processes are ranked. On macOS another user's process reports zero CPU " +
		"and zero memory without an error, so including them would sort the busiest processes " +
		"to the bottom. Run under sudo to rank everything.",
	Args: cobra.NoArgs,
	RunE: runTop,
}

func init() {
	// These defaults are the real ones: config.Load binds these flags, so a
	// value here is used only when neither the config file nor the environment
	// supplies one.
	topCmd.Flags().IntP("number", "n", top.FitWindow,
		fmt.Sprintf("how many processes to list (0 fills the terminal, or lists %d when there is none)", top.DefaultRows))
	topCmd.Flags().DurationP("interval", "d", top.DefaultInterval, "how long to measure for")
	topCmd.Flags().StringP("sort", "s", "cpu", "sort key: cpu | mem")
	topCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	topCmd.Flags().Bool("once", false, "print one frame and exit (automatic when output is not a terminal)")
	rootCmd.AddCommand(topCmd)
}

func runTop(cmd *cobra.Command, _ []string) error {
	outputFlag, _ := cmd.Flags().GetString("output")
	format, err := render.ParseFormat(outputFlag)
	if err != nil {
		return err
	}
	cfg, err := config.Load(cmd.Flags(), configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// config.Load resolved flag > env > file > default and validated the lot.
	sortKey, rows, interval := cfg.Top.Sort, cfg.Top.Number, cfg.Top.Interval

	opts := top.Options{Sort: sortKey, Owner: rankingOwner()}
	out := cmd.OutOrStdout()
	once, _ := cmd.Flags().GetBool("once")

	// An interactive view has nowhere to draw when output is piped or
	// redirected, so a non-terminal is itself a request for one frame.
	if format == render.FormatTable && !once && isTerminal(out) {
		home, _ := os.UserHomeDir()
		return tui.RunTop(cmd.Context(), top.Host{}, opts, cfg.Ignore, interval, home, rows)
	}

	// There is no window to fill, so an unasked-for count becomes the default.
	if rows == top.FitWindow {
		rows = top.DefaultRows
	}
	frame, err := rankOnce(top.Host{}, cfg.Ignore, interval, opts)
	if err != nil {
		return err
	}

	switch format {
	case render.FormatJSON:
		body, err := render.TopJSON(top.Top(frame.Rows, rows), frame.Totals)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		home, _ := os.UserHomeDir()
		fmt.Fprint(out, render.TopHeader(frame.Totals))
		fmt.Fprintln(out)
		fmt.Fprint(out, render.TopTable(top.Top(frame.Rows, rows), home, terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}
	return nil
}

// rankOnce measures for interval and returns one frame.
func rankOnce(m top.Machine, ignore []string, interval time.Duration, opt top.Options) (*top.Frame, error) {
	r := top.NewRanker(m, opt)
	r.Ignore = ignore
	if _, err := r.Next(time.Now()); err != nil {
		return nil, fmt.Errorf("reading processes: %w", err)
	}
	time.Sleep(interval)
	frame, err := r.Next(time.Now())
	if err != nil {
		return nil, fmt.Errorf("reading processes: %w", err)
	}
	if frame == nil {
		return nil, fmt.Errorf("no measurement after %s", interval)
	}
	return frame, nil
}

// rankingOwner restricts the ranking to the invoking user unless pcpm is
// running as root, which is the only way to read another user's figures.
func rankingOwner() top.Owner { return ownerForUID(os.Getuid()) }

// ownerForUID is rankingOwner's decision without the process's own identity, so
// that the root case can be exercised without being root.
func ownerForUID(uid int) top.Owner {
	if uid == 0 {
		return top.AnyOwner()
	}
	return top.OwnedBy(int32(uid))
}
