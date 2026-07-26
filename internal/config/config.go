// Package config resolves pcpm's runtime configuration from (in decreasing
// priority) command-line flags, PCPM_* environment variables, and a YAML config
// file.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

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
}

// WatchConfig is how often the collector works. Raising SampleInterval trades
// resolution for storage; lowering DiscoverInterval catches shorter-lived child
// processes at the cost of walking the whole process table more often.
type WatchConfig struct {
	SampleInterval   time.Duration
	DiscoverInterval time.Duration
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

	v.SetEnvPrefix("PCPM")
	v.AutomaticEnv()

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
	return Config{
		Ignore: ignore,
		Watch: WatchConfig{
			SampleInterval:   v.GetDuration("watch.sample_interval"),
			DiscoverInterval: v.GetDuration("watch.discover_interval"),
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
