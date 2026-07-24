package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/pflag"
)

// newFlags builds a flag set matching the orphans command's --min-uid / --ignore.
func newFlags(minUIDDefault int32) *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int32("min-uid", minUIDDefault, "")
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
	cfgPath := writeConfig(t, dir, "min_uid: 1500\nignore:\n  - from-file\n")

	t.Run("file value used when no flag or env", func(t *testing.T) {
		cfg, err := Load(newFlags(500), cfgPath, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MinUID != 1500 {
			t.Errorf("min_uid: want 1500 (file), got %d", cfg.MinUID)
		}
		if !slices.Equal(cfg.Ignore, []string{"from-file"}) {
			t.Errorf("ignore: want [from-file], got %v", cfg.Ignore)
		}
	})

	t.Run("missing file falls back to platform default, no error", func(t *testing.T) {
		cfg, err := Load(newFlags(500), filepath.Join(dir, "does-not-exist.yaml"), "darwin")
		if err != nil {
			t.Fatalf("missing file should not error: %v", err)
		}
		if cfg.MinUID != 500 {
			t.Errorf("min_uid: want 500 (darwin default), got %d", cfg.MinUID)
		}
		if len(cfg.Ignore) != 0 {
			t.Errorf("ignore: want empty, got %v", cfg.Ignore)
		}
	})

	t.Run("flag overrides file; --ignore adds to file list", func(t *testing.T) {
		fs := newFlags(500)
		_ = fs.Set("min-uid", "2000")
		_ = fs.Set("ignore", "from-flag")
		cfg, err := Load(fs, cfgPath, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MinUID != 2000 {
			t.Errorf("min_uid: want 2000 (flag over file), got %d", cfg.MinUID)
		}
		if !slices.Contains(cfg.Ignore, "from-file") || !slices.Contains(cfg.Ignore, "from-flag") {
			t.Errorf("ignore: want union of file+flag, got %v", cfg.Ignore)
		}
	})

	t.Run("env overrides file, flag beats env", func(t *testing.T) {
		t.Setenv("PCPM_MIN_UID", "3000")

		cfg, err := Load(newFlags(500), cfgPath, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MinUID != 3000 {
			t.Errorf("min_uid: want 3000 (env over file), got %d", cfg.MinUID)
		}

		fs := newFlags(500)
		_ = fs.Set("min-uid", "2000")
		cfg, err = Load(fs, cfgPath, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MinUID != 2000 {
			t.Errorf("min_uid: want 2000 (flag over env), got %d", cfg.MinUID)
		}
	})
}
