package watch

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

// Traffic is what one process has sent and received, as cumulative counters
// rather than rates — the same shape as CPUSeconds, for the same reason
// (ADR-0008).
//
// Unlike CPU time, this counter belongs to pcpm's reading of the machine rather
// than to the process: it starts at zero when the collector starts, so traffic
// from before then is not counted, and traffic during a period when the
// collector was not running is not merely unsampled but unknowable.
type Traffic struct {
	InBytes  int64
	OutBytes int64
}

// Add returns the two totals summed. Traffic is additive at every hop — across
// processes, across buckets, and across a re-aggregation to a coarser bucket —
// which is what lets a window's total be a sum rather than a difference.
func (t Traffic) Add(other Traffic) Traffic {
	return Traffic{InBytes: t.InBytes + other.InBytes, OutBytes: t.OutBytes + other.OutBytes}
}

// trafficHeader is the row the source emits at the start of every sample.
//
// It is checked rather than skipped. The format carries no compatibility
// promise — nettop is an Apple platform binary but, unlike netstat, its source
// is not published — and reading columns by position after they move produces
// confident wrong numbers, which is worse than no numbers (ADR-0012).
const trafficHeader = ",bytes_in,bytes_out,"

// accumulator turns the source's stream into monotonic per-process counters.
//
// The stream reports each process's bytes across the sockets the source is
// currently tracking, which falls when a connection closes. Accumulating the
// rises and ignoring the falls turns that into a counter that only grows, which
// is what the rest of the watch tool already knows how to store and chart.
type accumulator struct {
	// last is the figure each process was last reported at, which is what a new
	// reading is compared against. A PID absent from it has not been seen since
	// the source last started, so its first reading seeds rather than counts.
	last  map[int32]Traffic
	total map[int32]Traffic
	// sawHeader records that the format was confirmed. Checking only the lines
	// that look like a header leaves a hole: a stream that stopped emitting one
	// would be read column-by-position without complaint, which is the silent
	// wrongness the check exists to prevent.
	sawHeader bool
}

func newAccumulator() *accumulator {
	return &accumulator{
		last:  make(map[int32]Traffic),
		total: make(map[int32]Traffic),
	}
}

// feed takes one line of the source's output.
//
// Anything that is not a measurement — blank lines, the per-connection detail
// rows, rows whose figures are empty — is ignored rather than treated as an
// error. Only a header that does not match is refused, because that is the one
// case where carrying on would produce numbers that look right.
func (a *accumulator) feed(line string) error {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil
	}
	if strings.HasPrefix(line, ",") {
		if line != trafficHeader {
			return fmt.Errorf("%w: got header %q, expected %q "+
				"(columns can no longer be trusted by position)",
				ErrTrafficFormat, line, trafficHeader)
		}
		a.sawHeader = true
		return nil
	}
	if !a.sawHeader {
		return fmt.Errorf("%w: measurements arrived before any recognised header "+
			"(expected %q first)", ErrTrafficFormat, trafficHeader)
	}

	record, err := csv.NewReader(strings.NewReader(line)).Read()
	if err != nil || len(record) < 3 {
		return nil
	}
	pid, ok := pidFromSourceKey(record[0])
	if !ok {
		return nil
	}
	in, errIn := strconv.ParseInt(record[1], 10, 64)
	out, errOut := strconv.ParseInt(record[2], 10, 64)
	if errIn != nil || errOut != nil {
		return nil
	}

	now := Traffic{InBytes: in, OutBytes: out}
	if prev, seen := a.last[pid]; seen {
		total := a.total[pid]
		total.InBytes += rise(prev.InBytes, now.InBytes)
		total.OutBytes += rise(prev.OutBytes, now.OutBytes)
		a.total[pid] = total
	}
	a.last[pid] = now
	return nil
}

// restart forgets where each process was last seen, so that the first reading
// after the source is restarted seeds a new baseline instead of arriving as one
// enormous delta. The totals already accumulated are kept.
func (a *accumulator) restart() {
	a.last = make(map[int32]Traffic)
	// A new child must prove its format again rather than inherit the last
	// one's clean bill of health.
	a.sawHeader = false
}

// snapshot copies the counters out. The caller holds it while writing Samples,
// and a later reading must not change figures already being stored.
func (a *accumulator) snapshot() map[int32]Traffic {
	out := make(map[int32]Traffic, len(a.total))
	for pid, t := range a.total {
		out[pid] = t
	}
	return out
}

// rise is the increase between two readings, treating a fall as zero: a falling
// counter means a connection left the set being tracked, not negative traffic.
func rise(from, to int64) int64 {
	if to <= from {
		return 0
	}
	return to - from
}

// pidFromSourceKey pulls the PID out of the source's "name.pid" key.
//
// The name is truncated to fifteen characters and may itself contain dots and
// spaces — "python3.13.11072", "Adobe Desktop S.7057" — so the PID is what
// follows the *last* dot, not the first.
func pidFromSourceKey(key string) (int32, bool) {
	dot := strings.LastIndex(key, ".")
	if dot < 0 {
		return 0, false
	}
	pid, err := strconv.ParseInt(key[dot+1:], 10, 32)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return int32(pid), true
}
