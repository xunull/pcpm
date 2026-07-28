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
	// The defaults shown here are placeholders: configuration is resolved in
	// runTop, where a config file can raise them and an explicit flag still
	// wins. Declaring them keeps `--help` honest about the built-in values.
	topCmd.Flags().IntP("number", "n", top.DefaultRows, "how many processes to list")
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

	// Flags beat configuration, which beats the built-in defaults.
	sortKey, rows, interval := cfg.Top.Sort, cfg.Top.Number, cfg.Top.Interval
	if cmd.Flags().Changed("sort") {
		sortFlag, _ := cmd.Flags().GetString("sort")
		if sortKey, err = top.ParseSortKey(sortFlag); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("number") {
		rows, _ = cmd.Flags().GetInt("number")
	}
	if cmd.Flags().Changed("interval") {
		interval, _ = cmd.Flags().GetDuration("interval")
	}
	if interval <= 0 {
		return fmt.Errorf("invalid interval %s: a rate needs time to pass", interval)
	}
	if rows < 1 {
		return fmt.Errorf("invalid number of rows %d", rows)
	}

	opts := top.Options{Sort: sortKey, Owner: rankingOwner(), Limit: rows}
	out := cmd.OutOrStdout()
	once, _ := cmd.Flags().GetBool("once")

	// An interactive view has nowhere to draw when output is piped or
	// redirected, so a non-terminal is itself a request for one frame.
	if format == render.FormatTable && !once && isTerminal(out) {
		home, _ := os.UserHomeDir()
		// A reader who named a number gets that number; one who did not gets as
		// much of the machine as the window holds, the way top(1) behaves.
		fit := 0
		if cmd.Flags().Changed("number") {
			fit = rows
		}
		return tui.RunTop(cmd.Context(), top.Host{}, opts, interval, home, fit)
	}

	frame, err := rankOnce(top.Host{}, interval, opts)
	if err != nil {
		return err
	}

	switch format {
	case render.FormatJSON:
		body, err := render.TopJSON(frame.Rows, frame.Totals)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		home, _ := os.UserHomeDir()
		fmt.Fprint(out, render.TopHeader(frame.Totals))
		fmt.Fprintln(out)
		fmt.Fprint(out, render.TopTable(frame.Rows, home, terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}
	return nil
}

// rankOnce measures for interval and returns one frame.
func rankOnce(m top.Machine, interval time.Duration, opt top.Options) (*top.Frame, error) {
	r := top.NewRanker(m, opt)
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
func rankingOwner() top.Owner {
	uid := os.Getuid()
	if uid == 0 {
		return top.AnyOwner()
	}
	return top.OwnedBy(int32(uid))
}
