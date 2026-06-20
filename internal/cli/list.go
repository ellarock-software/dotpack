package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed resources",
		Long: `List every resource dotpack has installed, one line per record.
Output includes the full ID (the unambiguous uninstall handle), the
install scope, the install time, and the source URI.

Records are returned in manifest slot order — re-installs replace
records in place, so list output is stable across re-installs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd)
		},
	}
	return cmd
}

func runList(cmd *cobra.Command) error {
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	// List is a pure manifest read — no adapter required. orchestrator.Reader
	// is the type for adapter-free operations (List + Uninstall today).
	r := orchestrator.NewReader(d, mf)

	records, err := r.List()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		cmd.Println("no installs")
		return nil
	}
	for _, r := range records {
		// Single line per install: ID + scope + installed_at + source.
		// Format is "<id>\tscope=<scope>\tinstalled=<time>\tsource=<source>".
		// Tabs let users pipe through column / awk; we stay minimal
		// (advisor #8) until a --format flag becomes necessary.
		fmt.Fprintf(cmd.OutOrStdout(), "%s\tscope=%s\tinstalled=%s\tsource=%s\n",
			r.ID, r.Scope, r.InstalledAt, r.Source)
	}
	return nil
}
