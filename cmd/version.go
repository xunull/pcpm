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
	applyVersion()
}

// applyVersion points cobra's --version at the same text `pcpm version` prints:
// two ways of asking the same question should not give different answers.
func applyVersion() {
	rootCmd.Version = build.Version
	rootCmd.SetVersionTemplate(build.String())
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
	applyVersion()
	rootCmd.AddCommand(versionCmd)
}
