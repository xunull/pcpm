package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/pflag"
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
