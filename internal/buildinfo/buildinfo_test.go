package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestNewKeepsStampedValues(t *testing.T) {
	got := New("0.1.0", "1e8767c", "2026-07-25T06:00:00Z")

	if got.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0", got.Version)
	}
	if got.Commit != "1e8767c" {
		t.Errorf("Commit = %q, want 1e8767c", got.Commit)
	}
	if got.Date != "2026-07-25T06:00:00Z" {
		t.Errorf("Date = %q, want 2026-07-25T06:00:00Z", got.Date)
	}
	if got.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", got.GoVersion, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; got.Platform != want {
		t.Errorf("Platform = %q, want %q", got.Platform, want)
	}
}

// A plain `go build` stamps nothing; the command must still say something
// honest rather than printing blank fields.
func TestNewFillsPlaceholdersForAnUnstampedBuild(t *testing.T) {
	got := New("", "", "")

	if got.Version != "dev" {
		t.Errorf("Version = %q, want dev", got.Version)
	}
	if got.Commit == "" {
		t.Error("Commit is blank; want a placeholder")
	}
	if got.Date == "" {
		t.Error("Date is blank; want a placeholder")
	}
	// runtime-derived fields are always available
	if got.GoVersion == "" || got.Platform == "" {
		t.Errorf("runtime fields must always be set: %+v", got)
	}
}

func TestString(t *testing.T) {
	out := New("0.1.0", "1e8767c", "2026-07-25T06:00:00Z").String()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	if len(lines) != 5 {
		t.Fatalf("want 5 lines, got %d:\n%s", len(lines), out)
	}
	// headline carries the binary name and the version
	if !strings.HasPrefix(lines[0], Name+" ") || !strings.Contains(lines[0], "0.1.0") {
		t.Errorf("headline = %q, want %q followed by the version", lines[0], Name)
	}
	for _, want := range []string{"commit:", "built:", "go:", "platform:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing the %q field:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1e8767c") || !strings.Contains(out, "2026-07-25T06:00:00Z") {
		t.Errorf("output is missing the stamped commit/date:\n%s", out)
	}
}
