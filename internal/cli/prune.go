package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
)

func newReconcileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Report manifest drift",
		Long: `Compare dotpack's install manifest with the current filesystem.

Reconcile is read-only: it reports missing files or merged config keys
that dotpack still has provenance for. Use prune to remove manifest rows
whose recorded claims are all gone.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReconcile(cmd)
		},
	}
	return cmd
}

func newPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove stale manifest records",
		Long: `Remove manifest records whose recorded files and merged config keys
are all absent from disk.

Prune does not delete untracked host config entries. Without a manifest
claim, dotpack cannot prove ownership of arbitrary config fragments.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrune(cmd)
		},
	}
	return cmd
}

func runReconcile(cmd *cobra.Command) error {
	r, err := newManifestReader()
	if err != nil {
		return err
	}
	statuses, err := r.Reconcile()
	if err != nil {
		return err
	}
	if len(statuses) == 0 {
		cmd.Println("no installs")
		return nil
	}
	drift := 0
	for _, st := range statuses {
		if !st.HasDrift() {
			continue
		}
		drift++
		printReconcileStatus(cmd, st)
	}
	if drift == 0 {
		cmd.Println("manifest matches observed installs")
	}
	return nil
}

func runPrune(cmd *cobra.Command) error {
	r, err := newManifestReader()
	if err != nil {
		return err
	}
	result, err := r.PruneStaleRecords()
	if err != nil {
		return err
	}
	if len(result.Pruned) == 0 {
		cmd.Println("no stale installs")
	} else {
		for _, rec := range result.Pruned {
			cmd.Printf("Pruned %s\n", rec.ID)
		}
	}
	if len(result.Partial) > 0 {
		cmd.Printf("kept %d partially present %s; run dotpack reconcile for details\n",
			len(result.Partial), pluralInstall(len(result.Partial)))
	}
	return nil
}

func newManifestReader() (*orchestrator.Reader, error) {
	d, err := dirs.FromEnv()
	if err != nil {
		return nil, err
	}
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	return orchestrator.NewReader(d, mf), nil
}

func printReconcileStatus(cmd *cobra.Command, st orchestrator.ReconcileStatus) {
	cmd.Printf("%s\n", st.Record.ID)
	for _, p := range st.MissingFiles {
		cmd.Printf("  missing file %s\n", p)
	}
	for _, d := range st.DriftedFiles {
		cmd.Printf("  drifted file %s expected=%s actual=%s\n", d.Path, d.ExpectedSHA256, d.ActualSHA256)
	}
	for _, mk := range st.MissingMergedKeys {
		cmd.Printf("  missing merged key %s#%s\n", mk.File, mk.Path)
	}
	for _, msg := range st.Errors {
		cmd.Printf("  error %s\n", msg)
	}
}

func pluralInstall(n int) string {
	if n == 1 {
		return "install"
	}
	return "installs"
}
