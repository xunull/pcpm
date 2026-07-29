package watch

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// ErrTrafficUnsupported is returned where there is no way to measure
// per-process traffic without privilege. Linux is such a platform: every tool
// that does this there needs CAP_NET_RAW or root, which pcpm will not ask for.
var ErrTrafficUnsupported = errors.New("per-process traffic is not available on this platform")

// TrafficSource supplies cumulative per-process byte counters.
//
// Err reporting a problem is expected rather than exceptional: the source is an
// external program whose output format carries no compatibility promise. A
// caller must be able to tell "no traffic" from "no measurement", because in a
// chart those look identical and the reader will believe the zero.
type TrafficSource interface {
	Snapshot() map[int32]Traffic
	Err() error
	Close() error
}

// trafficSourceInterval is how often the source is asked to report.
//
// It runs faster than the collector samples so that every tick has a recent
// reading to hand rather than one it has to wait for. The cost is small — the
// stream is a few dozen short lines a second.
const trafficSourceInterval = 1

// reader runs the measuring program and accumulates what it prints.
type reader struct {
	mu   sync.Mutex
	acc  *accumulator
	err  error
	cmd  *exec.Cmd
	out  io.ReadCloser
	done chan struct{}
}

// ErrTrafficFormat marks a failure that retrying cannot fix. A program that has
// exited may well start again; one whose output no longer matches what pcpm can
// read will produce the same unreadable output every time.
var ErrTrafficFormat = errors.New("the traffic source's output format is not one pcpm can read")

// maxTrafficRestarts is how many times a source that keeps dying is restarted
// before pcpm stops trying. A source failing this persistently is not going to
// recover, and a restart loop would cost more than the measurement is worth.
const maxTrafficRestarts = 3

// StartTrafficSource begins measuring, keeping the source alive as long as it is
// worth keeping alive. A platform with no way to measure returns
// ErrTrafficUnsupported, which callers treat as "no traffic column" rather than
// as a failure to start.
func StartTrafficSource() (TrafficSource, error) {
	if newTrafficCommand(trafficSourceInterval) == nil {
		return nil, ErrTrafficUnsupported
	}
	return startSupervisor(func() *exec.Cmd { return newTrafficCommand(trafficSourceInterval) })
}

// supervisor keeps one traffic source running behind a stable handle.
//
// The accumulator lives here rather than in the reader, so that restarting the
// child keeps everything counted so far. A reader owning its own accumulator
// would silently reset every total the moment the source hiccuped.
type supervisor struct {
	mu       sync.Mutex
	acc      *accumulator
	newCmd   func() *exec.Cmd
	reader   *reader
	restarts int
	gaveUp   error
	closed   bool
}

func startSupervisor(newCmd func() *exec.Cmd) (*supervisor, error) {
	s := &supervisor{acc: newAccumulator(), newCmd: newCmd}
	r, err := startReader(newCmd(), s.acc)
	if err != nil {
		return nil, err
	}
	s.reader = r
	return s, nil
}

// ensure restarts a failed source, or gives up on one that will not recover.
//
// It runs when the collector asks for figures rather than on a timer of its own:
// a source is only worth restarting when something wants to read it, and this
// keeps the supervisor free of a goroutine that would outlive its usefulness.
func (s *supervisor) ensure() {
	// Without the closed check, reading a snapshot after Close would notice the
	// dead child and start a fresh one — resurrecting the process the caller
	// had just shut down.
	if s.closed || s.gaveUp != nil || s.reader == nil {
		return
	}
	failure := s.reader.Err()
	if failure == nil {
		return
	}

	_ = s.reader.Close()
	s.reader = nil

	if errors.Is(failure, ErrTrafficFormat) {
		s.gaveUp = failure
		return
	}
	s.restarts++
	if s.restarts > maxTrafficRestarts {
		s.gaveUp = fmt.Errorf("gave up after %d restarts: %w", maxTrafficRestarts, failure)
		return
	}
	// The child's counters begin again from zero, so forget where each process
	// was last seen; the totals already accumulated are kept.
	s.acc.restart()
	r, err := startReader(s.newCmd(), s.acc)
	if err != nil {
		s.gaveUp = fmt.Errorf("could not restart the traffic source: %w", err)
		return
	}
	s.reader = r
}

func (s *supervisor) Snapshot() map[int32]Traffic {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	if s.gaveUp != nil || s.reader == nil {
		return nil
	}
	return s.reader.Snapshot()
}

// Err reports only a source pcpm has stopped trying to keep alive. A child that
// died and was restarted is not a failure the caller needs to know about.
func (s *supervisor) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	return s.gaveUp
}

func (s *supervisor) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.reader == nil {
		return nil
	}
	err := s.reader.Close()
	s.reader = nil
	return err
}

func startReader(cmd *exec.Cmd, acc *accumulator) (*reader, error) {
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &reader{acc: acc, cmd: cmd, out: out, done: make(chan struct{})}
	go r.consume(out)
	return r, nil
}

// consume reads until the stream ends, which happens when the program exits.
//
// A header it does not recognise stops the reading and is remembered: carrying
// on would mean taking columns by position after they have moved, and the
// figures would look perfectly reasonable while being wrong.
func (r *reader) consume(out io.ReadCloser) {
	defer close(r.done)
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		r.mu.Lock()
		err := r.acc.feed(scanner.Text())
		r.mu.Unlock()
		if err != nil {
			r.fail(err)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		r.fail(err)
		return
	}
	r.fail(errors.New("the traffic source stopped reporting"))
}

func (r *reader) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *reader) Snapshot() map[int32]Traffic {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.acc.snapshot()
}

func (r *reader) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close stops the program and waits for it, so that nothing is left behind when
// the collector exits.
//
// The pipe is closed as well as the process killed. Killing alone is not enough:
// any descendant the program left behind inherits the pipe and holds it open,
// and the read would block until that descendant happened to exit — which a test
// caught taking thirty seconds.
func (r *reader) Close() error {
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.out != nil {
		_ = r.out.Close()
	}
	<-r.done
	_ = r.cmd.Wait()
	return nil
}
