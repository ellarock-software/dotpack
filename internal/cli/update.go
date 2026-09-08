package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/sourceregistry"
)

func newUpdateCmd() *cobra.Command {
	var (
		allowLossy bool
		force      bool
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh all registered repositories and update their skills",
		Long: `Refresh all registered skill repositories from ~/.dotpack/sources.yaml
across all saved destinations.

update takes no required arguments and runs from any working directory.
It discovers newly added skills, refreshes changed packages, and reconciles
upstream removals. Local modifications, target conflicts, and unapproved
packages are preserved and reported.`,
		Example: `  dotpack update
  dotpack update --allow-lossy
  dotpack update --skill-bypass-security custom-skill`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, allowLossy, force)
		},
	}

	cmd.Flags().BoolVar(&allowLossy, "allow-lossy", false, "Proceed even if the adapter cannot honour all source fields")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite untracked collisions at the install target")
	addSkillSecurityBypassFlag(cmd)
	return cmd
}

func runUpdate(cmd *cobra.Command, allowLossy, force bool) error {
	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}

	// Concurrency lock
	lock, err := sourceregistry.AcquireLock(d.DotpackHome)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	store := sourceregistry.NewStore(filepath.Join(d.DotpackHome, "sources.yaml"))
	reg, err := store.Load()
	if err != nil {
		return fmt.Errorf("update: load source registry: %w", err)
	}

	if len(reg.Sources) == 0 {
		cmd.Println("No registered repositories found. Use 'dotpack add <owner/repo>' to register a repository.")
		return nil
	}

	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))

	bypassNames := requestedSkillSecurityBypasses(cmd)
	evaluator := makeCLISecurityGateEvaluator(cmd)

	opts := sourceregistry.UpdateOptions{
		AllowLossy:          allowLossy,
		Force:               force,
		BypassSecurityNames: bypassNames,
		GateEvaluator:       evaluator,
	}

	var updateRes sourceregistry.UpdateResult

	// Process each source
	for i := range reg.Sources {
		src := &reg.Sources[i]
		srcRes := sourceregistry.SourceResult{
			SourceID: src.ID,
			Ref:      src.Ref,
		}

		parsed, err := sourceregistry.ParseSourceSpec(src.ID)
		if err != nil {
			parsed = sourceregistry.ParsedSource{
				ID:       src.ID,
				CloneURL: src.URL,
				Ref:      src.Ref,
			}
		} else {
			parsed.Ref = src.Ref
			if src.URL != "" {
				parsed.CloneURL = src.URL
			}
		}

		commitSHA, cacheDir, fetchErr := sourceregistry.FetchSource(cmd.Context(), parsed, d)
		if fetchErr != nil {
			srcRes.Err = fetchErr
			updateRes.Summary.Failed++
			updateRes.Sources = append(updateRes.Sources, srcRes)
			continue
		}

		srcRes.CandidateCommit = commitSHA
		src.LastFetchedCommit = commitSHA

		discovered, discErr := sourceregistry.DiscoverSkills(cacheDir, src.DiscoveryPath)
		if discErr != nil {
			srcRes.Err = discErr
			updateRes.Summary.Failed++
			updateRes.Sources = append(updateRes.Sources, srcRes)
			continue
		}

		// Update each target binding
		for ti := range src.Targets {
			target := &src.Targets[ti]
			tr := sourceregistry.ExecuteSourceTargetUpdate(
				src,
				target,
				commitSHA,
				cacheDir,
				discovered,
				reg.Sources,
				opts,
				d,
				mf,
			)

			*target = tr.Target
			srcRes.Targets = append(srcRes.Targets, tr)

			updateRes.Summary.Added += tr.Added
			updateRes.Summary.Updated += tr.Updated
			updateRes.Summary.Removed += tr.Removed
			updateRes.Summary.Steady += tr.Steady
			updateRes.Summary.Conflict += tr.Conflict
			updateRes.Summary.Blocked += tr.Blocked
			updateRes.Summary.Failed += tr.Failed
		}

		updateRes.Sources = append(updateRes.Sources, srcRes)
	}

	// Persist updated sources registry
	if err := store.Save(reg); err != nil {
		return fmt.Errorf("update: save registry: %w", err)
	}

	// Render report
	for _, sr := range updateRes.Sources {
		refStr := sr.Ref
		if refStr == "" {
			refStr = "default"
		}
		cmd.Printf("Repository %s (ref: %s, commit: %s)\n", sr.SourceID, refStr, shortDigest(sr.CandidateCommit))
		if sr.Err != nil {
			cmd.Printf("  ! error: %v\n", sr.Err)
			continue
		}
		for _, tr := range sr.Targets {
			cmd.Printf("  Target %s (scope: %s", tr.Target.Agent, tr.Target.Scope)
			if tr.Target.TargetRoot != "" {
				cmd.Printf(", root: %s", tr.Target.TargetRoot)
			}
			cmd.Printf(")\n")
			for _, sk := range tr.Skills {
				switch sk.Action {
				case sourceregistry.ActionAdded:
					cmd.Printf("    + added: %s\n", sk.Name)
				case sourceregistry.ActionUpdated:
					cmd.Printf("    ~ updated: %s\n", sk.Name)
				case sourceregistry.ActionRemoved:
					cmd.Printf("    - removed: %s\n", sk.Name)
				case sourceregistry.ActionUnchanged:
					cmd.Printf("    = unchanged: %s\n", sk.Name)
				case sourceregistry.ActionConflict:
					cmd.Printf("    ! conflict: %s (%s)\n", sk.Name, sk.Reason)
				case sourceregistry.ActionBlocked:
					cmd.Printf("    X blocked: %s (%s)\n", sk.Name, sk.Reason)
				case sourceregistry.ActionFailed:
					cmd.Printf("    ! failed: %s (%s)\n", sk.Name, sk.Reason)
				}
			}
		}
	}

	cmd.Printf("\nUpdate summary: %d added, %d updated, %d removed, %d unchanged, %d conflicts, %d blocked, %d failed\n",
		updateRes.Summary.Added,
		updateRes.Summary.Updated,
		updateRes.Summary.Removed,
		updateRes.Summary.Steady,
		updateRes.Summary.Conflict,
		updateRes.Summary.Blocked,
		updateRes.Summary.Failed,
	)

	if updateRes.HasFailures() {
		var reasons []string
		if updateRes.Summary.Blocked > 0 {
			reasons = append(reasons, fmt.Sprintf("%d skill(s) blocked by security gate", updateRes.Summary.Blocked))
		}
		if updateRes.Summary.Conflict > 0 {
			reasons = append(reasons, fmt.Sprintf("%d conflict(s)", updateRes.Summary.Conflict))
		}
		if updateRes.Summary.Failed > 0 {
			reasons = append(reasons, fmt.Sprintf("%d failure(s)", updateRes.Summary.Failed))
		}
		return fmt.Errorf("update completed with errors: %s", strings.Join(reasons, "; "))
	}

	return nil
}
