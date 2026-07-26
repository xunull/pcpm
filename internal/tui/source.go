package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xunull/pcpm/internal/proc"
	"github.com/xunull/pcpm/internal/watch"
)

// StoreSource serves the view from pcpm's database.
//
// Status is asked of the machine on every refresh rather than cached: a target
// that exits while the view is open should say so, not keep claiming to run.
type StoreSource struct {
	Store  *watch.Store
	Target watch.Target
}

func (s StoreSource) Status() (watch.Status, error) {
	procs, err := proc.Collect()
	if err != nil {
		return watch.Status{}, err
	}
	return watch.Status{Target: s.Target, Running: s.Target.Running(proc.NewIndex(procs))}, nil
}

func (s StoreSource) Series(from, to time.Time, bucket time.Duration) ([]watch.Point, error) {
	return s.Store.SeriesFor(s.Target.ID, from, to, bucket)
}

func (s StoreSource) Summary(from, to time.Time, bucket time.Duration) (watch.Summary, error) {
	return s.Store.Summary(s.Target.ID, from, to, bucket)
}

// Run opens the interactive view and blocks until the user quits.
func Run(ctx context.Context, source Source, home string, window int) error {
	program := tea.NewProgram(New(source, home, window),
		tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}
