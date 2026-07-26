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

var watchShowCmd = &cobra.Command{
	Use:   "show <pid>",
	Short: "Report what a Watch Target has been consuming",
	Long: "Report what a Watch Target and its tree have been consuming over a window, " +
		"with the per-process breakdown that says which part of the tree is responsible. " +
		"Works on a target whose processes have already exited — what it was doing before " +
		"it died is usually the question.",
	Args: cobra.ExactArgs(1),
	RunE: runWatchShow,
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
	watchDaemonCmd.Flags().Bool("stop", false, "stop a background collector instead of running one")
	watchAddCmd.Flags().Bool("no-daemon", false, "do not start the collector if it is not running")

	watchShowCmd.Flags().StringP("output", "o", "table", "output format: table | json")
	watchShowCmd.Flags().Duration("window", time.Hour, "how far back to report")
	watchShowCmd.Flags().Duration("bucket", 0,
		"time resolution to aggregate at (default: 1/120th of the window)")

	watchCmd.AddCommand(watchAddCmd, watchLsCmd, watchRmCmd, watchShowCmd, watchDaemonCmd)
	rootCmd.AddCommand(watchCmd)
}

func runWatchShow(cmd *cobra.Command, args []string) error {
	format, err := outputFormat(cmd)
	if err != nil {
		return err
	}
	pid, err := parsePID(args[0])
	if err != nil {
		return err
	}
	window, _ := cmd.Flags().GetDuration("window")
	if window <= 0 {
		return fmt.Errorf("--window must be positive")
	}
	bucket, _ := cmd.Flags().GetDuration("bucket")
	if bucket <= 0 {
		bucket = defaultBucket(window)
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
	target, err := latestTargetFor(targets, pid)
	if err != nil {
		return err
	}

	procs, err := proc.Collect()
	if err != nil {
		return fmt.Errorf("collecting processes: %w", err)
	}
	status := watch.Status{Target: target, Running: target.Running(proc.NewIndex(procs))}

	now := time.Now()
	summary, err := store.Summary(target.ID, now.Add(-window), now, bucket)
	if err != nil {
		return fmt.Errorf("summarising: %w", err)
	}

	out := cmd.OutOrStdout()
	switch format {
	case render.FormatJSON:
		body, err := render.WatchSummaryJSON(status, summary, window)
		if err != nil {
			return fmt.Errorf("rendering json: %w", err)
		}
		fmt.Fprint(out, body)
	case render.FormatTable:
		home, _ := os.UserHomeDir()
		fmt.Fprint(out, render.WatchSummaryText(status, summary, window, now, home, terminalWidth(out)))
	default:
		return fmt.Errorf("unhandled output format %v", format)
	}
	return nil
}

// defaultBucket picks a resolution from the window: enough points to show the
// shape, few enough that each is backed by real samples.
func defaultBucket(window time.Duration) time.Duration {
	const points = 120
	if b := window / points; b > time.Second {
		return b
	}
	return time.Second
}

// latestTargetFor finds the target for a PID. A PID can name more than one
// target once it has been recycled, so the most recently added wins — that is
// the one the user just looked at in `watch ls`.
func latestTargetFor(targets []watch.Target, pid int32) (watch.Target, error) {
	var found watch.Target
	for _, t := range targets {
		if t.PID == pid && (found.ID == 0 || t.AddedAt.After(found.AddedAt)) {
			found = t
		}
	}
	if found.ID == 0 {
		return watch.Target{}, fmt.Errorf("pcpm is not watching %d — add it with `pcpm watch add %d`", pid, pid)
	}
	return found, nil
}

func runWatchDaemon(cmd *cobra.Command, _ []string) error {
	if stopFlag, _ := cmd.Flags().GetBool("stop"); stopFlag {
		stopped, err := watch.StopDaemon(dbPath(cmd))
		if err != nil {
			return err
		}
		if stopped {
			fmt.Fprintln(cmd.OutOrStdout(), "asked the collector to stop")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "no collector is running")
		}
		return nil
	}

	cfg, err := config.Load(cmd.Flags(), configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// One collector per database, or two would write the same ticks.
	release, err := watch.AcquireDaemonLock(dbPath(cmd))
	if err != nil {
		return err
	}
	defer release()

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

	// Watching that stops when the terminal closes is not watching, so adding a
	// target starts the collector unless told not to. It is announced rather
	// than silent: a background process pcpm started must never become
	// something the user has forgotten about — which is, after all, the thing
	// this tool exists to find.
	started := int32(0)
	if noDaemon, _ := cmd.Flags().GetBool("no-daemon"); !noDaemon {
		if started, err = watch.StartDaemon(dbPath(cmd)); err != nil {
			return fmt.Errorf("starting the collector: %w", err)
		}
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
	if started > 0 {
		fmt.Fprintf(out, "started the collector in the background (pid %d); stop it with `pcpm watch daemon --stop`\n", started)
	}
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
		// A background process pcpm started must not become something the user
		// has forgotten about, so its state is reported where the targets are.
		fmt.Fprint(out, render.DaemonLine(store.Daemon(dbPath(cmd)), time.Now()))
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

// dbPath resolves where pcpm's database lives, honouring --db. It returns ""
// when there is no home directory to place it under.
func dbPath(cmd *cobra.Command) string {
	if path, _ := cmd.Flags().GetString("db"); path != "" {
		return path
	}
	if dir := config.StateDir(); dir != "" {
		return filepath.Join(dir, "pcpm.db")
	}
	return ""
}

// openStore opens pcpm's database, honouring --db.
func openStore(cmd *cobra.Command) (*watch.Store, error) {
	path := dbPath(cmd)
	if path == "" {
		return nil, errors.New("no home directory found, so pcpm cannot place its database; pass --db")
	}
	return watch.Open(path)
}
