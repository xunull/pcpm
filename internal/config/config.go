// Package config resolves pcpm's runtime configuration from (in decreasing
// priority) command-line flags, PCPM_* environment variables, a YAML config
// file, and built-in per-platform defaults.
package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/xunull/pcpm/internal/orphan"
)

// Config is the resolved configuration the orphans command runs against.
type Config struct {
	MinUID int32    // minimum uid treated as a real login user
	Ignore []string // glob patterns (by process name) to suppress from output
}

// Load resolves configuration with precedence flag > env (PCPM_*) > config file
// > built-in default. For ignore, viper resolves the configured list (env else
// file else default) and any --ignore flags are then appended to it — flags add
// to the configured list, they do not replace it. A missing config file is not
// an error. explicitPath, when non-empty, is used verbatim; otherwise the
// default per-user config directory is searched. goos selects the default
// min_uid (see orphan.DefaultMinUID).
func Load(flags *pflag.FlagSet, explicitPath, goos string) (Config, error) {
	v := viper.New()
	v.SetDefault("min_uid", int(orphan.DefaultMinUID(goos)))
	v.SetDefault("ignore", []string{})

	v.SetEnvPrefix("PCPM")
	v.AutomaticEnv()

	// Only min_uid is bound to the flag: viper then gives us
	// flag(changed) > env > file > default for it. ignore is unioned by hand
	// below so --ignore adds to, rather than replaces, the configured list.
	if f := flags.Lookup("min-uid"); f != nil {
		_ = v.BindPFlag("min_uid", f)
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
	if flags.Changed("ignore") {
		flagIgnore, _ := flags.GetStringArray("ignore")
		ignore = append(ignore, flagIgnore...)
	}

	return Config{
		MinUID: int32(v.GetInt("min_uid")),
		Ignore: ignore,
	}, nil
}

// DefaultDir is the per-user directory pcpm searches for config.yaml:
// $XDG_CONFIG_HOME/pcpm, or ~/.config/pcpm when XDG_CONFIG_HOME is unset.
func DefaultDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "pcpm")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "" // no known home directory; the caller skips this search path
	}
	return filepath.Join(home, ".config", "pcpm")
}
