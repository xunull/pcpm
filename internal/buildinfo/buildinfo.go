// Package buildinfo describes which build of pcpm is running: the version it
// was stamped with at release time, plus the toolchain and platform it was
// built for. pcpm is a diagnostic tool, so "which build produced this output"
// is the first thing worth being able to answer.
package buildinfo

import (
	"fmt"
	"runtime"
	"strings"
)

// Name is the binary's name, used in the version headline.
const Name = "pcpm"

// Placeholders stand in for a build that was not stamped — a plain `go build`
// rather than a release build.
const (
	unknownVersion = "dev"
	unknownCommit  = "none"
	unknownDate    = "unknown"
)

// Info identifies a build of pcpm.
type Info struct {
	Version   string // release version, or "dev" for an unstamped build
	Commit    string // commit the build came from
	Date      string // build timestamp
	GoVersion string // toolchain that built it
	Platform  string // GOOS/GOARCH it was built for
}

// New assembles the build's identity from the values stamped in via ldflags at
// release time, filling in placeholders for anything a plain `go build` left
// empty and deriving the toolchain and platform from the runtime.
func New(version, commit, date string) Info {
	return Info{
		Version:   orPlaceholder(version, unknownVersion),
		Commit:    orPlaceholder(commit, unknownCommit),
		Date:      orPlaceholder(date, unknownDate),
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// String renders the build's identity as a headline plus one labelled field
// per line.
func (i Info) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", Name, i.Version)
	fmt.Fprintf(&b, "commit:   %s\n", i.Commit)
	fmt.Fprintf(&b, "built:    %s\n", i.Date)
	fmt.Fprintf(&b, "go:       %s\n", i.GoVersion)
	fmt.Fprintf(&b, "platform: %s\n", i.Platform)
	return b.String()
}

// orPlaceholder returns s, or placeholder when s is empty or blank.
func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}
	return s
}
