package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate"
	"github.com/ellarock-software/dotpack/internal/skillgate/delta"
)

type approveSkillOptions struct {
	layout       sourceLayoutOptions
	reason       string
	skillNames   []string
	all          bool
	asJSON       bool
	acceptAtRisk bool
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

Approving refuses by default when it would absorb a finding at or above the
gate's severity floor, and prints those findings instead. Pass
--accept-at-risk-findings to record them deliberately. Without that, a bulk
--all run would quietly launder exactly the findings the gate exists to stop.

Baselines are written to <policy-root>/.dotpack/skillgate/baselines/<skill>.json,
where the policy root is the source's own repository, or --skill-policy-root.`,
		Example: `  dotpack approve-skill .agents --skill code-review
  dotpack approve-skill .agents --all --reason "Reviewed in PR #42"
  dotpack approve-skill github:OWNER/REPO --all --skill-policy-root .
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
	cmd.Flags().StringVar(&opts.reason, "reason", "", "Reason recorded in the baseline, e.g. a pull-request reference")
	cmd.Flags().BoolVar(&opts.acceptAtRisk, "accept-at-risk-findings", false,
		"Record findings at or above the gate's severity floor. Without this, approving refuses and prints them")
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

	// approve-skill writes a delta-gate baseline. Under any other gate the
	// file it produces would be inert, so say so rather than writing one.
	if gate := currentSkillGate(); gate != delta.Name {
		return fmt.Errorf("approve-skill records %s baselines, but the selected gate is %q; approvals for that gate are managed elsewhere", delta.Name, gate)
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

	// resolvePolicyRoot already honours --skill-policy-root, so passing
	// the source root here yields the operator's choice when they made one.
	policyRoot, trusted, err := resolvePolicyRoot(selection.SourceRoot, d)
	if err != nil {
		return err
	}
	if policyRoot == "" {
		return fmt.Errorf("approve-skill: no policy root resolved for %s; pass --%s to say which repository owns the approval", source, skillPolicyRootFlag)
	}
	// Writing an approval into a source dotpack fetched would persist it
	// into a throwaway cache under DotpackHome, where the gate would then
	// read it back as though it had been reviewed and committed.
	if !trusted {
		var b strings.Builder
		fmt.Fprintf(&b, "approve-skill: refusing to write approvals into %s, which is dotpack-managed state rather than a repository you control\n", policyRoot)
		fmt.Fprintf(&b, "Name your own repository instead:\n")
		fmt.Fprintf(&b, "    dotpack approve-skill %s --%s .", source, skillPolicyRootFlag)
		return errors.New(b.String())
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
		AtRisk        int    `json:"at_risk_findings,omitempty"`
	}
	var approvals []approval
	results := make([]delta.PackageResult, 0, len(targets))

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
		results = append(results, result)
	}

	// Refuse the whole run before writing anything if it would absorb a
	// finding at or above the floor. Approving is meant to record a
	// reviewed state, not to silence the gate -- and a bulk --all run in
	// response to a block is exactly where silencing happens by accident.
	if !opts.acceptAtRisk {
		if err := refuseAtRiskApproval(results); err != nil {
			return err
		}
	}

	for _, result := range results {
		if err := gate.Approve(result, detectorVersion, opts.reason); err != nil {
			return err
		}
		approvals = append(approvals, approval{
			Skill:         result.Skill,
			BaselinePath:  result.BaselinePath,
			ContentSHA256: result.Evaluation.ContentSHA256,
			Findings:      result.Evaluation.TotalFindings,
			AtRisk:        len(result.Evaluation.Blocking),
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
		risk := ""
		if a.AtRisk > 0 {
			risk = fmt.Sprintf(", INCLUDING %d at or above the severity floor", a.AtRisk)
		}
		cmd.Printf("APPROVED %s: %d finding(s) baselined%s, durable content %s\n",
			a.Skill, a.Findings, risk, shortDigest(a.ContentSHA256))
	}
	cmd.Printf("\nBaselines written under %s\n", delta.BaselineDir(policyRoot))
	cmd.Println("Review the diff before committing: an approval is a security decision.")
	return nil
}

// refuseAtRiskApproval blocks an approval that would absorb findings at
// or above the gate's floor, and prints them so the operator can decide
// deliberately rather than by omission.
func refuseAtRiskApproval(results []delta.PackageResult) error {
	var atRisk []delta.PackageResult
	for _, r := range results {
		if len(r.Evaluation.Blocking) > 0 {
			atRisk = append(atRisk, r)
		}
	}
	if len(atRisk) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "approve-skill: refusing to approve %d package(s) that would record findings at or above the severity floor:\n", len(atRisk))
	for _, r := range atRisk {
		fmt.Fprintf(&b, "\n  %s\n", r.Skill)
		for _, f := range r.Evaluation.Blocking {
			loc := f.File
			if f.Line != nil {
				loc = fmt.Sprintf("%s:%d", f.File, *f.Line)
			} else if len(f.Lines) > 0 {
				loc = fmt.Sprintf("%s:%v", f.File, f.Lines)
			}
			fmt.Fprintf(&b, "    [%s] %s  %s\n", f.Severity, f.RuleID, loc)
			if f.Title != "" {
				fmt.Fprintf(&b, "             %s\n", f.Title)
			}
		}
	}
	b.WriteString("\nNothing was written. Review these, then either fix the package or record them deliberately:\n")
	b.WriteString("    dotpack approve-skill ... --accept-at-risk-findings --reason \"<why this is acceptable>\"")
	return errors.New(b.String())
}

func shortDigest(d string) string {
	if len(d) <= 12 {
		return d
	}
	return strings.ToLower(d[:12])
}
