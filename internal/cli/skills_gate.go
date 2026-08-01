package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/skillgate"
	"github.com/ellarock-software/dotpack/internal/skillgate/registry"
	"github.com/ellarock-software/dotpack/internal/skillspector"
)

var mandatorySkillScan = runMandatorySkillScan
var ensureSkillSpectorRuntime = skillspector.EnsureRuntime

// runMandatorySkillScan is the single enforcement funnel every
// skill-bearing command passes through. It selects the operator's gate,
// resolves the policy root once so every gate agrees on it, and turns a
// refusal into the error that aborts the command.
//
// The registry lookup lives INSIDE this function on purpose. The
// mandatorySkillScan var above is stubbed package-wide by
// testmain_test.go; if gate selection moved above that seam, every CLI
// test would start provisioning a Python environment.
func runMandatorySkillScan(command string, selection skillScanSelection, d dirs.Dirs) error {
	if len(selection.Targets) == 0 && len(selection.SecurityBypassed) == 0 {
		return nil
	}

	gateName := currentSkillGate()
	gate, err := registry.Build(gateName, d)
	if err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}

	policyRoot, trusted, err := resolvePolicyRoot(selection.SourceRoot, d)
	if err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}

	verdict, err := gate.Run(context.Background(), skillgate.Request{
		Command:           command,
		Selection:         selection,
		PolicyRoot:        policyRoot,
		PolicyRootTrusted: trusted,
	})
	if err != nil {
		return fmt.Errorf("%s: %s gate: %w", command, gateName, err)
	}
	if !verdict.Pass {
		return gateBlockedError(command, gateName, verdict)
	}
	return nil
}

// gateBlockedError renders a refusal.
//
// The per-package detail is included rather than left in the artifact
// because an operator who cannot see WHY a package was refused, and what
// to do about it, reaches for --skill-bypass-security. A bypass is
// permanent and covers the whole package, which is a worse outcome than
// whatever the gate caught.
func gateBlockedError(command, gateName string, verdict skillgate.Verdict) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s gate blocked %d skill package(s)", command, gateName, len(verdict.Blocked))
	if verdict.Reason != "" {
		fmt.Fprintf(&b, ": %s", verdict.Reason)
	}
	for _, blocked := range verdict.Blocked {
		fmt.Fprintf(&b, "\n\nBLOCKED  %s: %s", blocked.Skill, blocked.Reason)
		for _, line := range blocked.Detail {
			fmt.Fprintf(&b, "\n         %s", line)
		}
	}
	if verdict.ArtifactPath != "" {
		fmt.Fprintf(&b, "\n\nFull report: %s", verdict.ArtifactPath)
	}
	return errors.New(b.String())
}

func runMandatorySkillScanWithSecurityBypasses(cmd *cobra.Command, command string, selection skillScanSelection, bypassNames []string, d dirs.Dirs) error {
	filtered, err := applySkillSecurityBypasses(selection, bypassNames)
	if err != nil {
		return err
	}
	reportSkillSecurityBypasses(cmd, filtered.SecurityBypassed)
	return mandatorySkillScan(command, filtered, d)
}

func ensureMandatorySkillScanForSource(cmd *cobra.Command, command, source string, bypassNames []string, d dirs.Dirs) error {
	selection, err := resolveSkillScanSelection(source, sourceLayoutOptions{}, nil, false, "HEAD", d)
	if err != nil {
		return err
	}
	return runMandatorySkillScanWithSecurityBypasses(cmd, command, selection, bypassNames, d)
}

func ensureMandatorySkillScanForSourceLayout(cmd *cobra.Command, command string, layout sourceLayout, bypassNames []string, d dirs.Dirs) error {
	return ensureMandatorySkillScanForSkillRoot(cmd, command, layout.root, layout.kindDir(resource.KindSkill), bypassNames, d)
}

func ensureMandatorySkillScanForSkillRoot(cmd *cobra.Command, command, sourceRoot, skillRoot string, bypassNames []string, d dirs.Dirs) error {
	selection, err := buildSkillScanSelection(sourceRoot, skillRoot)
	if err != nil {
		return err
	}
	return runMandatorySkillScanWithSecurityBypasses(cmd, command, selection, bypassNames, d)
}

func ensureMandatorySkillScanForSkillFiles(cmd *cobra.Command, command string, skillFiles []string, sourceRoot string, bypassNames []string, d dirs.Dirs) error {
	selection, err := buildSkillScanSelectionForFiles(skillFiles, sourceRoot)
	if err != nil {
		return err
	}
	return runMandatorySkillScanWithSecurityBypasses(cmd, command, selection, bypassNames, d)
}

func buildSkillScanSelection(sourceRoot, skillRoot string) (skillScanSelection, error) {
	targets, err := discoverSkillScanTargets(sourceRoot, skillRoot)
	if err != nil {
		return skillScanSelection{}, err
	}
	return skillScanSelection{
		SourceRoot: sourceRoot,
		SkillRoot:  skillRoot,
		Targets:    targets,
	}, nil
}

func buildSkillScanSelectionForFiles(skillFiles []string, sourceRoot string) (skillScanSelection, error) {
	targets := make([]skillScanTarget, 0, len(skillFiles))
	seen := make(map[string]string, len(skillFiles))
	for _, skillFile := range skillFiles {
		target, err := buildSkillScanTarget(skillFile, sourceRoot)
		if err != nil {
			return skillScanSelection{}, err
		}
		if prior, ok := seen[target.Name]; ok {
			return skillScanSelection{}, fmt.Errorf("duplicate skill name %q at %s and %s", target.Name, prior, target.SkillDir)
		}
		seen[target.Name] = target.SkillDir
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return skillScanSelection{
		SourceRoot: sourceRoot,
		SkillRoot:  sourceRoot,
		Targets:    targets,
	}, nil
}
