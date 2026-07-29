package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/pflag"
	"strings"
	"time"

	"github.com/xunull/pcpm/internal/top"
)

// newFlags builds a flag set matching the commands' --ignore flag.
func newFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.StringArray("ignore", nil, "")
	return fs
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, "ignore:\n  - from-file\n  - \"*.helper\"\n")

	t.Run("file list used when no flag", func(t *testing.T) {
		cfg, err := Load(newFlags(), cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cfg.Ignore, []string{"from-file", "*.helper"}) {
			t.Errorf("ignore: want [from-file *.helper], got %v", cfg.Ignore)
		}
	})

	t.Run("missing file is not an error", func(t *testing.T) {
		cfg, err := Load(newFlags(), filepath.Join(dir, "does-not-exist.yaml"))
		if err != nil {
			t.Fatalf("missing file should not error: %v", err)
		}
		if len(cfg.Ignore) != 0 {
			t.Errorf("ignore: want empty, got %v", cfg.Ignore)
		}
	})

	t.Run("--ignore adds to the file list rather than replacing it", func(t *testing.T) {
		fs := newFlags()
		_ = fs.Set("ignore", "from-flag")
		cfg, err := Load(fs, cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(cfg.Ignore, "from-file") || !slices.Contains(cfg.Ignore, "from-flag") {
			t.Errorf("ignore: want the union of file and flag, got %v", cfg.Ignore)
		}
	})

	t.Run("repeated --ignore all land", func(t *testing.T) {
		fs := newFlags()
		_ = fs.Set("ignore", "a")
		_ = fs.Set("ignore", "b")
		cfg, err := Load(fs, cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(cfg.Ignore, "a") || !slices.Contains(cfg.Ignore, "b") {
			t.Errorf("ignore: want both a and b, got %v", cfg.Ignore)
		}
	})
}

func TestTopDefaults(t *testing.T) {
	cfg, err := Load(nil, filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Top.Interval != top.DefaultInterval {
		t.Errorf("interval = %s, want %s", cfg.Top.Interval, top.DefaultInterval)
	}
	// Nobody asked for a count, so the window decides. A one-shot, having no
	// window, falls back to top.DefaultRows.
	if cfg.Top.Number != top.FitWindow {
		t.Errorf("number = %d, want FitWindow", cfg.Top.Number)
	}
	if cfg.Top.Sort != top.ByCPU {
		t.Errorf("sort = %v, want ByCPU", cfg.Top.Sort)
	}
}

func TestTopReadsItsSectionFromTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("top:\n  interval: 3s\n  number: 25\n  sort: mem\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Top.Interval != 3*time.Second {
		t.Errorf("interval = %s, want 3s", cfg.Top.Interval)
	}
	if cfg.Top.Number != 25 {
		t.Errorf("number = %d, want 25", cfg.Top.Number)
	}
	if cfg.Top.Sort != top.ByMemory {
		t.Errorf("sort = %v, want ByMemory", cfg.Top.Sort)
	}
}

// A setting that cannot be honoured must say which one it was. Quietly falling
// back to the default leaves a reader wondering why their file has no effect.
func TestBadTopSettingsFailByName(t *testing.T) {
	for _, tc := range []struct{ name, body, wants string }{
		{"sort", "top:\n  sort: sideways\n", "top.sort"},
		{"number", "top:\n  number: -3\n", "top.number"},
		{"interval", "top:\n  interval: 0s\n", "top.interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(nil, path)
			if err == nil {
				t.Fatalf("%s should have failed", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("error %q does not name %q", err, tc.wants)
			}
		})
	}
}

// A measurement that must be found in a config file before it does anything is
// a measurement nobody has.
func TestTrafficCollectionIsOnByDefault(t *testing.T) {
	cfg, err := Load(nil, filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Watch.Network {
		t.Error("traffic collection should be on unless turned off")
	}
}

func TestTrafficCollectionCanBeTurnedOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("watch:\n  network: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Watch.Network {
		t.Error("network: false was ignored")
	}
}

// An Interval shorter than a sample takes to gather makes pcpm a significant
// part of what it is measuring — enough CPU, at 100ms, to rank itself.
func TestAnIntervalTooShortToMeasureInIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("top:\n  interval: 100ms\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(nil, path)

	if err == nil {
		t.Fatal("an interval below the minimum was accepted")
	}
	if !strings.Contains(err.Error(), "top.interval") {
		t.Errorf("error %q does not name the setting", err)
	}
	// "invalid" would leave a reader guessing at what to type instead.
	if !strings.Contains(err.Error(), top.MinInterval.String()) {
		t.Errorf("error %q does not say what the minimum is", err)
	}
}

// The boundary belongs to the accepted side, or the message names a value that
// is itself refused.
func TestTheMinimumIntervalIsItselfAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "top:\n  interval: " + top.MinInterval.String() + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(nil, path)

	if err != nil {
		t.Fatalf("the minimum interval was refused: %v", err)
	}
	if cfg.Top.Interval != top.MinInterval {
		t.Errorf("interval = %v, want %v", cfg.Top.Interval, top.MinInterval)
	}
}

// The three ways to set an Interval must be refused alike. Validating after the
// sources are resolved is what makes that true, and this pins it: a check moved
// into any one reader would leave the other two open.
func TestAShortIntervalIsRefusedWhicheverWaySetIt(t *testing.T) {
	tooShort := (top.MinInterval - time.Millisecond).String()

	t.Run("flag", func(t *testing.T) {
		flags := pflag.NewFlagSet("top", pflag.ContinueOnError)
		flags.Duration("interval", top.DefaultInterval, "")
		if err := flags.Set("interval", tooShort); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(flags, filepath.Join(t.TempDir(), "none.yaml")); err == nil {
			t.Error("a short interval given as a flag was accepted")
		}
	})

	t.Run("environment", func(t *testing.T) {
		t.Setenv("PCPM_TOP_INTERVAL", tooShort)
		if _, err := Load(nil, filepath.Join(t.TempDir(), "none.yaml")); err == nil {
			t.Error("a short interval given in the environment was accepted")
		}
	})
}

// Every nested setting was unreachable from the environment: viper builds the
// variable name from the key verbatim, so it looked for PCPM_TOP.INTERVAL — a
// name no shell will export, a dot not being valid in an identifier. The
// documented resolution order promised otherwise, for every key but the
// top-level one that happens to have no dot in it.
func TestNestedSettingsCanBeSetFromTheEnvironment(t *testing.T) {
	t.Setenv("PCPM_TOP_INTERVAL", "3s")
	t.Setenv("PCPM_TOP_NUMBER", "7")
	t.Setenv("PCPM_WATCH_SAMPLE_INTERVAL", "11s")
	t.Setenv("PCPM_WATCH_NETWORK", "false")

	cfg, err := Load(nil, filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Top.Interval != 3*time.Second {
		t.Errorf("top.interval = %v, want 3s", cfg.Top.Interval)
	}
	if cfg.Top.Number != 7 {
		t.Errorf("top.number = %d, want 7", cfg.Top.Number)
	}
	if cfg.Watch.SampleInterval != 11*time.Second {
		t.Errorf("watch.sample_interval = %v, want 11s", cfg.Watch.SampleInterval)
	}
	if cfg.Watch.Network {
		t.Error("watch.network = true, want the environment's false")
	}
}

// The top-level key worked before the replacer and must still work after it.
func TestATopLevelSettingStillComesFromTheEnvironment(t *testing.T) {
	t.Setenv("PCPM_IGNORE", "gbrain node")

	cfg, err := Load(nil, filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ignore) == 0 {
		t.Error("PCPM_IGNORE reached nothing")
	}
}
