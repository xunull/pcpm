package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/xunull/pcpm/internal/render"
	"github.com/xunull/pcpm/internal/top"
)

// DefaultInterval is how long the ranking watches before reporting.
//
// It is both the sampling window and, later, the refresh period: a rate is the
// difference between two readings divided by the time between them, so the
// figure shown is by construction the average over exactly this long. One
// second follows top(1), and is short enough that the wait before the first
// frame is not itself annoying.
const DefaultInterval = time.Second

// DefaultRows is how many processes to rank.
const DefaultRows = 10

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
	topCmd.Flags().IntP("number", "n", DefaultRows, "how many processes to list")
	topCmd.Flags().DurationP("interval", "d", DefaultInterval, "how long to measure for")
	topCmd.Flags().StringP("sort", "s", "cpu", "sort key: cpu | mem")
	topCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	rootCmd.AddCommand(topCmd)
}

func runTop(cmd *cobra.Command, _ []string) error {
	outputFlag, _ := cmd.Flags().GetString("output")
	format, err := render.ParseFormat(outputFlag)
	if err != nil {
		return err
	}
	sortFlag, _ := cmd.Flags().GetString("sort")
	sortKey, err := top.ParseSortKey(sortFlag)
	if err != nil {
		return err
	}
	rows, _ := cmd.Flags().GetInt("number")
	interval, _ := cmd.Flags().GetDuration("interval")
	if interval <= 0 {
		return fmt.Errorf("invalid interval %s: a rate needs time to pass", interval)
	}

	ranked, err := rankOnce(top.Host{}, interval, top.Options{
		Sort:  sortKey,
		Owner: rankingOwner(),
		Limit: rows,
	})
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	switch format {
	case render.FormatJSON:
		body, err := render.TopJSON(ranked)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		fmt.Fprint(out, render.TopTable(ranked, terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}
	return nil
}

// rankOnce measures for interval and returns one ranking.
func rankOnce(m top.Machine, interval time.Duration, opt top.Options) ([]top.Process, error) {
	r := top.NewRanker(m, opt)
	if _, err := r.Next(time.Now()); err != nil {
		return nil, fmt.Errorf("reading processes: %w", err)
	}
	time.Sleep(interval)
	ranked, err := r.Next(time.Now())
	if err != nil {
		return nil, fmt.Errorf("reading processes: %w", err)
	}
	return ranked, nil
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
