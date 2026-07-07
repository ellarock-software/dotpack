package cli

import (
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Version is stamped at release build time via -ldflags (see .goreleaser.yaml).
// For binaries built with `go install ...@vX.Y.Z` it stays "dev", so we fall back
// to the module version recorded in the binary's build info.
var Version = "dev"

// resolveVersion returns the release version when set via ldflags, otherwise the
// module version from build info (with a leading "v" trimmed to match GoReleaser's
// output), otherwise "dev".
func resolveVersion() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return "dev"
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print dotpack version",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("dotpack %s\n", resolveVersion())
		},
	}
}
