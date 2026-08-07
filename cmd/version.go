package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

/*
Builds made without the Makefile's -ldflags -X injection (go install,
plain go build) keep the placeholder metadata from root.go. The Go
toolchain still stamps every binary with module build info, so recover
what we can from it: the module version for proxy installs, and VCS
revision/time for builds from a checkout.
*/
func init() {
	if Version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		Version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value
			if len(Commit) > 7 {
				Commit = Commit[:7]
			}
		case "vcs.time":
			Date = s.Value
		}
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionLine())
			return err
		},
	}
}
