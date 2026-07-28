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
