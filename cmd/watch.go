package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/xunull/pcpm/internal/config"
	"github.com/xunull/pcpm/internal/proc"
	"github.com/xunull/pcpm/internal/render"
	"github.com/xunull/pcpm/internal/watch"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch what a process and its tree consume over time",
	Long: "Watch a process tree's resource use over time. `pcpm watch add <pid>` starts " +
		"watching one; the collector records what it and its descendants consume, so you " +
		"can later ask what it was doing — including after it has exited.",
}

var watchAddCmd = &cobra.Command{
	Use:   "add <pid>",
	Short: "Start watching a process and its tree",
	Long: "Start watching a process and its descendants. The process is pinned by when it " +
		"started as well as by its PID, so a later process that reuses the number does not " +
		"inherit this one's history.",
	Args: cobra.ExactArgs(1),
	RunE: runWatchAdd,
}

var watchLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List Watch Targets",
	Long: "List every Watch Target. WATCHING says whether pcpm is still collecting; " +
		"PROCESS says whether the process is still on the machine. They are separate: a " +
		"target's history is kept after its process exits, and watching can be stopped " +
		"while the process runs on.",
	Args: cobra.NoArgs,
	RunE: runWatchLs,
}

var watchRmCmd = &cobra.Command{
	Use:     "rm <pid>",
	Aliases: []string{"remove", "stop"},
	Short:   "Stop watching a process",
	Long: "Stop watching a process. What was already collected is kept — asking what it " +
		"was doing beforehand is usually the point.",
	Args: cobra.ExactArgs(1),
	RunE: runWatchRm,
}

var watchDaemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the collector in the foreground",
	Long: "Run the collector in the foreground, measuring every Watch Target until " +
		"interrupted. It re-reads which targets to measure from the database each tick, " +
		"so targets added or stopped elsewhere take effect within one tick.",
	Args: cobra.NoArgs,
	RunE: runWatchDaemon,
}

func init() {
	watchCmd.PersistentFlags().String("db", "",
		"database file (default: $XDG_STATE_HOME/pcpm/pcpm.db)")
	watchAddCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	watchLsCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	watchDaemonCmd.Flags().Duration("sample-interval", 0,
		"how often to measure (default: 5s, or watch.sample_interval in config)")
	watchDaemonCmd.Flags().Duration("discover-interval", 0,
		"how often to re-walk the process table for tree members (default: 30s)")
	watchDaemonCmd.Flags().Bool("quiet", false, "do not report each tick")

	watchCmd.AddCommand(watchAddCmd, watchLsCmd, watchRmCmd, watchDaemonCmd)
	rootCmd.AddCommand(watchCmd)
}

func runWatchDaemon(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(cmd.Flags(), configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	store, err := openStore(cmd)
	if err != nil {
		return err
	}
	defer store.Close()

	collector := watch.NewCollector(store, watch.Host{})
	collector.SampleInterval = cfg.Watch.SampleInterval
	collector.DiscoverInterval = cfg.Watch.DiscoverInterval
	if d, _ := cmd.Flags().GetDuration("sample-interval"); d > 0 {
		collector.SampleInterval = d
	}
	if d, _ := cmd.Flags().GetDuration("discover-interval"); d > 0 {
		collector.DiscoverInterval = d
	}

	out := cmd.OutOrStdout()
	if quiet, _ := cmd.Flags().GetBool("quiet"); !quiet {
		collector.Report = func(line string) { fmt.Fprintln(out, line) }
	}

	// Interrupt cancels the context, so the run stops between ticks and never
	// mid-write. Stopping the notifier restores default signal handling, so a
	// second interrupt still kills a wedged process.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(out, "collecting every %s, re-walking the process table every %s; interrupt to stop\n",
		collector.SampleInterval, collector.DiscoverInterval)
	if err := collector.Run(ctx); err != nil {
		return fmt.Errorf("collecting: %w", err)
	}
	fmt.Fprintln(out, "stopped")
	return nil
}

func runWatchAdd(cmd *cobra.Command, args []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	pid, err := parsePID(args[0])
	if err != nil {
		return err
	}

	procs, err := proc.Collect()
	if err != nil {
		return fmt.Errorf("collecting processes: %w", err)
	}
	target, ok := proc.NewIndex(procs).Lookup(pid)
	if !ok {
		return fmt.Errorf("no process with pid %d is running", pid)
	}

	store, err := openStore(cmd)
	if err != nil {
		return err
	}
	defer store.Close()

	added, err := store.AddTarget(watch.Target{
		PID:     target.PID,
		Created: target.Created,
		Name:    target.Name,
		Cmdline: target.Cmdline,
		Cwd:     target.Cwd,
	}, time.Now())
	if err != nil {
		return fmt.Errorf("storing the target: %w", err)
	}

	out := cmd.OutOrStdout()
	if format == render.FormatJSON {
		body, err := render.WatchTargetJSON(watch.Status{Target: added, Running: true})
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
		return nil
	}
	fmt.Fprintf(out, "watching %d (%s)\n", added.PID, added.Name)
	return nil
}

func runWatchLs(cmd *cobra.Command, _ []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}

	store, err := openStore(cmd)
	if err != nil {
		return err
	}
	defer store.Close()

	targets, err := store.Targets()
	if err != nil {
		return fmt.Errorf("reading targets: %w", err)
	}

	// Whether each target's process is still there is not stored — it is asked
	// of the machine, so the answer cannot go stale.
	procs, err := proc.Collect()
	if err != nil {
		return fmt.Errorf("collecting processes: %w", err)
	}
	statuses := watch.Statuses(targets, proc.NewIndex(procs))

	out := cmd.OutOrStdout()
	switch format {
	case render.FormatJSON:
		body, err := render.WatchTargetsJSON(statuses)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		home, _ := os.UserHomeDir()
		fmt.Fprint(out, render.WatchTargetsTable(statuses, time.Now(), home, terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}
	return nil
}

func runWatchRm(cmd *cobra.Command, args []string) error {
	pid, err := parsePID(args[0])
	if err != nil {
		return err
	}

	store, err := openStore(cmd)
	if err != nil {
		return err
	}
	defer store.Close()

	stopped, err := store.StopTarget(pid, time.Now())
	if err != nil {
		return fmt.Errorf("stopping the target: %w", err)
	}

	out := cmd.OutOrStdout()
	if stopped == 0 {
		// Not an error: the caller wanted this pid unwatched, and it is.
		fmt.Fprintf(out, "pcpm was not watching %d\n", pid)
		return nil
	}
	fmt.Fprintf(out, "stopped watching %d; what was collected is kept\n", pid)
	return nil
}

// outputFormat resolves the --output flag.
func outputFormat(cmd *cobra.Command) (render.Format, error) {
	flag, _ := cmd.Flags().GetString("output")
	return render.ParseFormat(flag)
}

// parsePID reads a PID argument, rejecting anything that cannot be one.
func parsePID(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a pid", s)
	}
	return int32(n), nil
}

// openStore opens pcpm's database, honouring --db.
func openStore(cmd *cobra.Command) (*watch.Store, error) {
	path, _ := cmd.Flags().GetString("db")
	if path == "" {
		dir := config.StateDir()
		if dir == "" {
			return nil, errors.New("no home directory found, so pcpm cannot place its database; pass --db")
		}
		path = filepath.Join(dir, "pcpm.db")
	}
	return watch.Open(path)
}
