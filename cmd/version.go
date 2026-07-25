package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/xunull/pcpm/internal/buildinfo"
)

// build identifies the running binary. main fills it in from the values stamped
// via ldflags at release time; until then it describes an unstamped build.
var build = buildinfo.New("", "", "")

// SetBuildInfo records the build stamps ldflags injected into main, so both
// `pcpm version` and `pcpm --version` report them.
func SetBuildInfo(version, commit, date string) {
	build = buildinfo.New(version, commit, date)
	rootCmd.Version = build.Version
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version, commit and platform of this build",
	Long: "Print which build of pcpm is running: its release version, the commit and time " +
		"it was built from, the Go toolchain that built it, and the platform it targets.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), build.String())
		return err
	},
}

func init() {
	rootCmd.Version = build.Version
	rootCmd.AddCommand(versionCmd)
}
