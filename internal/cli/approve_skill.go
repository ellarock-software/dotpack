package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate"
	"github.com/ellarock-software/dotpack/internal/skillgate/delta"
)

type approveSkillOptions struct {
	layout     sourceLayoutOptions
	policyRoot string
	reason     string
	skillNames []string
	all        bool
	asJSON     bool
}

func newApproveSkillCmd() *cobra.Command {
	var opts approveSkillOptions
	cmd := &cobra.Command{
		Use:   "approve-skill [source]",
		Short: "Approve skill packages at their current reviewed state",
		Long: `Record the current security state of a skill package as approved.

The delta gate blocks a package until it has an approved baseline, and
then blocks only findings that are NEW relative to that approval. This is
how a package earns its first approval, and how you accept a change after
reviewing it.

Approving is not a way to silence a gate. It records exactly what the
detector saw, with the detector version, policy version and timestamp that
produced it, into a file meant to be reviewed in a pull request. Read the
findings before you approve them.

Baselines are written to <policy-root>/.dotpack/skillgate/baselines/<skill>.json.`,
		Example: `  dotpack approve-skill .agents --skill code-review
  dotpack approve-skill .agents --all --reason "Reviewed in PR #42"
  dotpack approve-skill . --skill code-review --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := "."
			if len(args) == 1 {
				source = args[0]
			}
			return runApproveSkill(cmd, source, opts)
		},
	}
	addSkillScanLayoutFlags(cmd, &opts.layout)
	cmd.Flags().StringArrayVar(&opts.skillNames, "skill", nil, "Approve a named skill; repeatable")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Approve every skill in the source")
	cmd.Flags().StringVar(&opts.policyRoot, "policy-root", "", "Repository that owns the approvals (default: the source's own root)")
	cmd.Flags().StringVar(&opts.reason, "reason", "", "Reason recorded in the baseline, e.g. a pull-request reference")
	cmd.Flags().BoolVar(&opts.asJSON, "json", false, "Emit a machine-readable summary")

	// Deliberately no --skill-bypass-security here, matching
	// baseline-skills: bypassing a gate and recording an approval are
	// opposite operations, and offering both on one command invites
	// using the wrong one.
	return cmd
}

func runApproveSkill(cmd *cobra.Command, source string, opts approveSkillOptions) error {
	// Exactly one of --skill / --all. Requiring the choice explicitly
	// stops "approve everything" from being the accidental default when a
	// --skill flag is forgotten or misspelled.
	switch {
	case opts.all && len(opts.skillNames) > 0:
		return fmt.Errorf("approve-skill: pass either --skill or --all, not both")
	case !opts.all && len(opts.skillNames) == 0:
		return fmt.Errorf("approve-skill: pass --skill <name> (repeatable) or --all; approving every skill must be explicit")
	}

	d, err := dirs.FromEnv()
	if err != nil {
		return err
	}

	selection, err := resolveSkillScanSelection(source, opts.layout, opts.skillNames, false, "HEAD", d)
	if err != nil {
		return err
	}
	if len(selection.Targets) == 0 {
		return fmt.Errorf("approve-skill: no skills selected under %s", source)
	}

	policyRoot, trusted, err := resolvePolicyRoot(selection.SourceRoot, d)
	if err != nil {
		return err
	}
	if opts.policyRoot != "" {
		policyRoot, trusted, err = resolvePolicyRoot(opts.policyRoot, d)
		if err != nil {
			return err
		}
	}
	if policyRoot == "" {
		return fmt.Errorf("approve-skill: no policy root resolved for %s; pass --policy-root to say which repository owns the approval", source)
	}
	// Writing an approval into a source dotpack fetched would persist it
	// into a throwaway cache under DotpackHome, where the gate would then
	// read it back as though it had been reviewed and committed.
	if !trusted {
		return fmt.Errorf("approve-skill: refusing to write approvals into %s, which is dotpack-managed state rather than a repository you control; pass --policy-root to name the repository that owns this approval", policyRoot)
	}

	gate := delta.New(d)
	gate.ToolVersion = resolveVersion()

	ctx := context.Background()
	runtimeInfo, err := gate.EnsureRuntime(ctx)
	if err != nil {
		return err
	}
	detectorVersion := runtimeInfo.Metadata.DetectorVersion()

	targets := append([]skillgate.Target(nil), selection.Targets...)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })

	type approval struct {
		Skill         string `json:"skill"`
		BaselinePath  string `json:"baseline_path"`
		ContentSHA256 string `json:"content_sha256"`
		Findings      int    `json:"findings"`
	}
	var approvals []approval

	for _, target := range targets {
		result := gate.Inspect(ctx, target, policyRoot, runtimeInfo)

		// A package that could not be inspected is never approvable. An
		// approval must describe something that was actually read.
		if result.Evaluation.Hash.HashedFiles == 0 {
			return fmt.Errorf("approve-skill: %s: %s", target.Name, result.Reason)
		}
		if len(result.UnsafeLinks) > 0 {
			return fmt.Errorf("approve-skill: %s: %s", target.Name, result.Reason)
		}

		if err := gate.Approve(result, detectorVersion, opts.reason); err != nil {
			return err
		}
		approvals = append(approvals, approval{
			Skill:         target.Name,
			BaselinePath:  result.BaselinePath,
			ContentSHA256: result.Evaluation.ContentSHA256,
			Findings:      result.Evaluation.TotalFindings,
		})
	}

	if opts.asJSON {
		raw, err := json.MarshalIndent(map[string]any{
			"policy_root":      policyRoot,
			"detector_version": detectorVersion,
			"approvals":        approvals,
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("encode approve-skill summary: %w", err)
		}
		cmd.Println(string(raw))
		return nil
	}

	for _, a := range approvals {
		cmd.Printf("APPROVED %s: %d finding(s) baselined, durable content %s\n",
			a.Skill, a.Findings, shortDigest(a.ContentSHA256))
	}
	cmd.Printf("\nBaselines written under %s\n", delta.BaselineDir(policyRoot))
	cmd.Println("Review the diff before committing: an approval is a security decision.")
	return nil
}

func shortDigest(d string) string {
	if len(d) <= 12 {
		return d
	}
	return strings.ToLower(d[:12])
}
