package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/manifest"
	"github.com/ellarock/dotpack/internal/orchestrator"
)

func newUninstallCmd() *cobra.Command {
	var (
		agentName string
		kindName  string
	)

	cmd := &cobra.Command{
		Use:   "uninstall <name-or-id>",
		Short: "Remove an installed resource from an agent host",
		Long: `Remove an installed resource: deletes the files dotpack wrote, drops the
empty target directory if no other files remain, and removes the manifest
record.

Accepts either a full ID (host:kind:name) or a short name composed with
--agent and --kind:

  dotpack uninstall claude-code:skill:my-skill
  dotpack uninstall my-skill --agent claude-code --kind skill

Missing files are tolerated (idempotent re-uninstall finishes a previously
crashed run). Unknown IDs are a loud error.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(cmd, args[0], agentName, kindName)
		},
	}

	cmd.Flags().StringVar(&agentName, "agent", "claude-code", "Target host adapter (used when arg is a short name)")
	cmd.Flags().StringVar(&kindName, "kind", "skill", "Resource kind (used when arg is a short name)")
	return cmd
}

func runUninstall(cmd *cobra.Command, handle, agentName, kindName string) error {
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}

	id, err := resolveUninstallID(handle, agentName, kindName)
	if err != nil {
		return err
	}

	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	// Uninstall doesn't traverse an adapter today — the manifest record
	// carries absolute paths and the host identity is encoded in the ID.
	// orchestrator.Reader is the adapter-free type for List + Uninstall.
	// This split also avoids the second-adapter footgun the original
	// nil-adapter convention dodged: a full-ID handle whose host doesn't
	// match --agent's default would have spuriously failed adapter
	// construction (hostile-review #1 of slice 3 task #7). With Reader,
	// no adapter is constructed at all on the uninstall path.
	r := orchestrator.NewReader(d, mf)

	res, err := r.Uninstall(id)
	if err != nil {
		return err
	}

	cmd.Printf("Uninstalled %s\n", res.Record.ID)
	for _, p := range res.RemovedPaths {
		cmd.Printf("  removed %s\n", p)
	}
	for _, p := range res.MissingPaths {
		cmd.Printf("  skipped %s (already gone)\n", p)
	}
	if res.Record.TargetDir != "" {
		if res.TargetDirRemoved {
			cmd.Printf("  removed directory %s\n", res.Record.TargetDir)
		} else {
			cmd.Printf("  kept directory %s (not empty)\n", res.Record.TargetDir)
		}
	}
	return nil
}

// resolveUninstallID accepts either a full host:kind:name ID or a bare
// name; in the latter case it composes the ID from --agent and --kind.
// A handle containing ":" is treated as a full ID verbatim — the user
// is being explicit, so a typo'd --agent flag must not silently reshape
// an ID the user pasted in full.
func resolveUninstallID(handle, agentName, kindName string) (string, error) {
	if strings.Contains(handle, ":") {
		return handle, nil
	}
	if agentName == "" || kindName == "" {
		return "", fmt.Errorf("uninstall %q: provide either a full ID (host:kind:name) or --agent/--kind", handle)
	}
	// Funnel through resolveKind's switch so unknown --kind values
	// fail the same way `dotpack install --kind=bogus` does.
	if _, err := resolveKind(kindName, ""); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s:%s", agentName, kindName, handle), nil
}
