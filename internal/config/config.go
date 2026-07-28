// Package config resolves pcpm's runtime configuration from (in decreasing
// priority) command-line flags, PCPM_* environment variables, and a YAML config
// file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/xunull/pcpm/internal/top"
	"github.com/xunull/pcpm/internal/watch"
)

// Config is the resolved configuration pcpm's commands run against.
type Config struct {
	// Ignore holds glob patterns matched against a process name. A match is
	// suppressed from output — how a user silences a long-running job they
	// keep on purpose.
	Ignore []string

	// Watch is the collector's schedule.
	Watch WatchConfig

	// Top is how the CPU ranking behaves by default.
	Top TopConfig
}

// TopConfig is what `pcpm top` does when asked for nothing in particular.
//
// Interval is both the refresh period and the window each figure averages over
// — they are the same measurement, so they cannot be set apart. Raising it
// steadies the ordering at the cost of noticing a change later, and of a longer
// wait before a one-shot answers.
//
// Number is top.FitWindow (0) unless someone asked for a count, by flag or by
// file; the two are equivalent by design.
type TopConfig struct {
	Interval time.Duration
	Number   int
	Sort     top.SortKey
}

// WatchConfig is how often the collector works. Raising SampleInterval trades
// resolution for storage; lowering DiscoverInterval catches shorter-lived child
// processes at the cost of walking the whole process table more often.
type WatchConfig struct {
	SampleInterval   time.Duration
	DiscoverInterval time.Duration

	// MaintenanceInterval is how often to roll up settled Samples and drop what
	// has aged out. RawRetention and RollupRetention are how long each
	// resolution is kept: raw Samples answer "what exactly happened yesterday
	// afternoon", rollups answer "has this been creeping up for a fortnight" at
	// a fraction of the rows.
	MaintenanceInterval time.Duration
	RollupInterval      time.Duration
	RawRetention        time.Duration
	RollupRetention     time.Duration
}

// Load resolves configuration with precedence flag > env (PCPM_*) > config file
// > built-in default. For ignore, viper resolves the configured list (env else
// file else default) and any --ignore flags are then appended to it — flags add
// to the configured list, they do not replace it. A missing config file is not
// an error. explicitPath, when non-empty, is used verbatim; otherwise the
// default per-user config directory is searched.
func Load(flags *pflag.FlagSet, explicitPath string) (Config, error) {
	v := viper.New()
	v.SetDefault("ignore", []string{})
	// The collector's defaults live with the collector, so config and code
	// cannot drift apart on what "the default" is.
	v.SetDefault("watch.sample_interval", watch.DefaultSampleInterval)
	v.SetDefault("watch.discover_interval", watch.DefaultDiscoverInterval)
	v.SetDefault("watch.maintenance_interval", watch.DefaultMaintenanceInterval)
	v.SetDefault("watch.rollup_interval", watch.DefaultRollupInterval)
	v.SetDefault("watch.raw_retention", watch.DefaultRawRetention)
	v.SetDefault("watch.rollup_retention", watch.DefaultRollupRetention)
	v.SetDefault("top.interval", top.DefaultInterval)
	v.SetDefault("top.number", top.FitWindow)
	v.SetDefault("top.sort", "cpu")

	v.SetEnvPrefix("PCPM")
	v.AutomaticEnv()

	// Binding the flags here keeps one resolution order for everything. Doing
	// it in the command would mean a second, hand-rolled precedence rule that
	// could disagree with this one — and a second place to validate.
	if flags != nil {
		for key, flag := range map[string]string{
			"top.interval": "interval",
			"top.number":   "number",
			"top.sort":     "sort",
		} {
			if f := flags.Lookup(flag); f != nil {
				_ = v.BindPFlag(key, f)
			}
		}
	}

	if explicitPath != "" {
		v.SetConfigFile(explicitPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		if dir := DefaultDir(); dir != "" {
			v.AddConfigPath(dir)
		}
	}
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
		// A missing config file is fine — fall back to env/flags/defaults.
	}

	ignore := append([]string{}, v.GetStringSlice("ignore")...)
	if flags != nil && flags.Changed("ignore") {
		flagIgnore, _ := flags.GetStringArray("ignore")
		ignore = append(ignore, flagIgnore...)
	}
	sortKey, err := top.ParseSortKey(v.GetString("top.sort"))
	if err != nil {
		return Config{}, fmt.Errorf("top.sort: %w", err)
	}
	if n := v.GetInt("top.number"); n < 0 {
		return Config{}, fmt.Errorf("top.number: %d is not a number of rows to show", n)
	}
	if d := v.GetDuration("top.interval"); d <= 0 {
		return Config{}, fmt.Errorf("top.interval: %s leaves no time for a rate to be measured", d)
	}

	return Config{
		Ignore: ignore,
		Top: TopConfig{
			Interval: v.GetDuration("top.interval"),
			Number:   v.GetInt("top.number"),
			Sort:     sortKey,
		},
		Watch: WatchConfig{
			SampleInterval:      v.GetDuration("watch.sample_interval"),
			DiscoverInterval:    v.GetDuration("watch.discover_interval"),
			MaintenanceInterval: v.GetDuration("watch.maintenance_interval"),
			RollupInterval:      v.GetDuration("watch.rollup_interval"),
			RawRetention:        v.GetDuration("watch.raw_retention"),
			RollupRetention:     v.GetDuration("watch.rollup_retention"),
		},
	}, nil
}

// DefaultDir is the per-user directory pcpm searches for config.yaml:
// $XDG_CONFIG_HOME/pcpm, or ~/.config/pcpm when XDG_CONFIG_HOME is unset.
// It returns "" when no home directory is known.
func DefaultDir() string {
	return userDir("XDG_CONFIG_HOME", ".config")
}

// StateDir is the per-user directory pcpm keeps data it generates in — as
// opposed to configuration the user writes: $XDG_STATE_HOME/pcpm, or
// ~/.local/state/pcpm when XDG_STATE_HOME is unset. It returns "" when no home
// directory is known.
func StateDir() string {
	return userDir("XDG_STATE_HOME", ".local", "state")
}

// userDir resolves an XDG directory: the named environment variable when set,
// otherwise the fallback path relative to the home directory.
func userDir(env string, fallback ...string) string {
	if x := os.Getenv(env); x != "" {
		return filepath.Join(x, "pcpm")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(append(append([]string{home}, fallback...), "pcpm")...)
}
