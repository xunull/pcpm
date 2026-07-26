package watch

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/xunull/pcpm/internal/proc"
)

// Defaults for how often the collector works. Sampling is cheap — measured at
// ~106µs for a ten-process tree — while discovering tree membership means
// walking the whole process table, measured at ~27ms. They are therefore
// separate intervals rather than one: sampling can be frequent because
// discovery is not.
//
// The cost of discovering only every 30s is that a process which lives and dies
// between two passes is never seen. That is accepted: it does not change the
// answer to "is this target doing anything".
const (
	DefaultSampleInterval   = 5 * time.Second
	DefaultDiscoverInterval = 30 * time.Second
)

// Usage is what one process has consumed.
type Usage struct {
	CPUSeconds float64 // cumulative, not a rate (ADR-0008)
	RSSBytes   int64
}

// Machine is the host as the collector needs it: a way to see every process,
// and a way to measure one. It is an interface so the collector's scheduling can
// be tested without depending on what happens to be running.
type Machine interface {
	Snapshot() ([]proc.Process, error)
	Usage(pid int32) (Usage, error)
}

// Host measures the machine pcpm is running on.
type Host struct{}

// Snapshot enumerates every process on the host.
func (Host) Snapshot() ([]proc.Process, error) { return proc.Collect() }

// Usage reads one process's cumulative CPU time and resident memory.
//
// It deliberately does not use gopsutil's CPUPercent: that returns cumulative
// CPU divided by process age — a lifetime average, measured reporting 14.46%
// for a process actually consuming 26.5% — which draws a flat line (ADR-0008).
func (Host) Usage(pid int32) (Usage, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return Usage{}, err
	}
	times, err := p.Times()
	if err != nil {
		return Usage{}, err
	}
	u := Usage{CPUSeconds: times.User + times.System}
	// Memory can be unreadable when a process exits mid-tick; the CPU counter
	// is still worth recording, so this degrades rather than failing the tick.
	if mem, err := p.MemoryInfo(); err == nil && mem != nil {
		u.RSSBytes = int64(mem.RSS)
	}
	return u, nil
}

// Collector measures Watch Targets on a schedule and stores what it finds.
//
// It holds no list of its own: every tick it re-reads which targets to measure
// from the database, so a target added or stopped in another terminal takes
// effect within one tick and needs no IPC (ADR-0009).
type Collector struct {
	store   *Store
	machine Machine

	SampleInterval   time.Duration
	DiscoverInterval time.Duration

	// Maintenance is how often to roll up and drop what has aged out. It runs
	// far less often than sampling: it exists to keep long windows fast and the
	// database bounded, neither of which changes by the second.
	MaintenanceInterval time.Duration
	RollupInterval      time.Duration
	RawRetention        time.Duration
	RollupRetention     time.Duration

	// Now is the clock, injectable so tests need not sleep.
	Now func() time.Time
	// Report receives one line per tick. nil discards them.
	Report func(string)

	lastMaintenance time.Time

	// members caches each target's tree between discovery passes, keyed by
	// target ID. It holds the processes, not just their PIDs: a Sample records
	// the identity of the process it describes, which is only known here.
	members       map[int64][]proc.Process
	lastDiscovery time.Time
}

// NewCollector returns a collector measuring the given machine into the store.
func NewCollector(store *Store, machine Machine) *Collector {
	return &Collector{
		store:               store,
		machine:             machine,
		SampleInterval:      DefaultSampleInterval,
		DiscoverInterval:    DefaultDiscoverInterval,
		MaintenanceInterval: DefaultMaintenanceInterval,
		RollupInterval:      DefaultRollupInterval,
		RawRetention:        DefaultRawRetention,
		RollupRetention:     DefaultRollupRetention,
		Now:                 time.Now,
		members:             make(map[int64][]proc.Process),
	}
}

// Tick is one pass of collection: re-read the targets, refresh tree membership
// if it is due, measure, store. Splitting this out from Run is what lets the
// schedule be tested without waiting for wall-clock time.
func (c *Collector) Tick() error {
	now := c.Now()

	targets, err := c.store.WatchedTargets()
	if err != nil {
		return fmt.Errorf("reading targets: %w", err)
	}
	if len(targets) == 0 {
		c.report("no targets")
		return nil
	}

	if c.discoveryDue(now, targets) {
		if err := c.discover(targets); err != nil {
			return fmt.Errorf("discovering tree members: %w", err)
		}
		c.lastDiscovery = now
	}

	sampled, ended := 0, 0
	for _, t := range targets {
		alive := c.sampleTarget(t, now)
		if alive == 0 {
			// Every process in the tree is gone. Record when, and stop
			// measuring it; what it did beforehand stays.
			if err := c.store.MarkEnded(t.ID, now); err != nil {
				return fmt.Errorf("marking target %d ended: %w", t.ID, err)
			}
			delete(c.members, t.ID)
			ended++
			continue
		}
		sampled += alive
	}

	c.report(fmt.Sprintf("%s  %d target(s), %d process(es) sampled, %d ended",
		now.Format("15:04:05"), len(targets), sampled, ended))

	// Maintenance last: a failure to tidy up must not cost the tick's samples,
	// which have already been stored.
	if err := c.maintain(now); err != nil {
		return fmt.Errorf("maintenance: %w", err)
	}
	return nil
}

// maintain rolls up settled Samples and drops what has aged out, on its own
// slow schedule. Without it, long-window queries degrade into full table scans
// and the database grows without bound (ADR-0007).
func (c *Collector) maintain(now time.Time) error {
	if c.MaintenanceInterval <= 0 {
		return nil
	}
	if !c.lastMaintenance.IsZero() && now.Sub(c.lastMaintenance) < c.MaintenanceInterval {
		return nil
	}
	c.lastMaintenance = now

	// Roll up only what can no longer change: a bucket still receiving samples
	// would be summarised too early and then have to be corrected.
	settled := now.Add(-c.RollupInterval)
	rolled, err := c.store.Rollup(time.Time{}, settled, c.RollupInterval)
	if err != nil {
		return err
	}
	dropped, err := c.store.Retain(now, c.RawRetention, c.RollupRetention)
	if err != nil {
		return err
	}
	if rolled > 0 || dropped > 0 {
		c.report(fmt.Sprintf("%s  maintenance: %d bucket(s) rolled up, %d row(s) dropped",
			now.Format("15:04:05"), rolled, dropped))
	}
	return nil
}

// discoveryDue reports whether tree membership should be refreshed: on the
// normal interval, or immediately for a target that has never been walked.
func (c *Collector) discoveryDue(now time.Time, targets []Target) bool {
	if c.lastDiscovery.IsZero() || now.Sub(c.lastDiscovery) >= c.DiscoverInterval {
		return true
	}
	for _, t := range targets {
		if _, known := c.members[t.ID]; !known {
			return true
		}
	}
	return false
}

// discover refreshes every target's tree membership from one process-table
// snapshot, so N targets still cost a single walk.
func (c *Collector) discover(targets []Target) error {
	snapshot, err := c.machine.Snapshot()
	if err != nil {
		return err
	}
	ix := proc.NewIndex(snapshot)

	fresh := make(map[int64][]proc.Process, len(targets))
	for _, t := range targets {
		// A PID present but started at another time is a different process that
		// reused the number; its tree is not this target's.
		if !t.Running(ix) {
			fresh[t.ID] = nil
			continue
		}
		for _, pid := range ix.TreeMembers(t.PID) {
			if p, ok := ix.Lookup(pid); ok {
				fresh[t.ID] = append(fresh[t.ID], p)
			}
		}
	}
	c.members = fresh
	return nil
}

// sampleTarget measures every process still in a target's tree, returning how
// many were measurable. Zero means the tree is gone.
func (c *Collector) sampleTarget(t Target, now time.Time) int {
	members := c.members[t.ID]
	if len(members) == 0 {
		return 0
	}

	samples := make([]Sample, 0, len(members))
	for _, member := range members {
		usage, err := c.machine.Usage(member.PID)
		if err != nil {
			// The process exited between discovery and now. Not an error: the
			// next discovery pass will drop it from the tree.
			continue
		}
		samples = append(samples, Sample{
			PID:        member.PID,
			Created:    member.Created,
			Name:       member.Name,
			CPUSeconds: usage.CPUSeconds,
			RSSBytes:   usage.RSSBytes,
		})
	}
	if len(samples) == 0 {
		return 0
	}
	if err := c.store.SaveSamples(t.ID, now, samples); err != nil {
		c.report(fmt.Sprintf("storing samples for target %d: %v", t.ID, err))
		return len(samples)
	}
	return len(samples)
}

// Run collects until ctx is cancelled, returning nil on a clean shutdown. A
// failing tick stops the run: the alternative is a daemon that looks alive
// while silently recording nothing.
func (c *Collector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.SampleInterval)
	defer ticker.Stop()

	if err := c.Tick(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := c.Tick(); err != nil {
				return err
			}
		}
	}
}

func (c *Collector) report(line string) {
	if c.Report != nil {
		c.Report(line)
	}
}
