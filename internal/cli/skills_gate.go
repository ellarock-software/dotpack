package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/skillspector"
)

var mandatorySkillScan = runMandatorySkillScan
var ensureSkillSpectorRuntime = skillspector.EnsureRuntime

func runMandatorySkillScan(command string, selection skillScanSelection, d dirs.Dirs) error {
	if len(selection.Targets) == 0 && len(selection.SecurityBypassed) == 0 {
		return nil
	}

	runDir, err := createSkillSpectorRunDir(d.DotpackHome, sanitizeSkillGateCommand(command))
	if err != nil {
		return fmt.Errorf("%s: create SkillSpector run directory: %w", command, err)
	}

	baselineDir, err := resolveAutomaticBaselineDir(selection.SourceRoot)
	if err != nil {
		return fmt.Errorf("%s: resolve automatic baseline directory: %w", command, err)
	}

	var (
		runtimeInfo skillspector.Runtime
		results     []skillScanResult
	)
	if len(selection.Targets) > 0 {
		runtimeInfo, err = ensureSkillSpectorRuntime(d.DotpackHome)
		if err != nil {
			return fmt.Errorf("%s: ensure SkillSpector runtime: %w", command, err)
		}
		results, _, err = runSkillScansWithOptionalBaselines(selection.Targets, runDir, baselineDir, "json", runtimeInfo)
		if err != nil {
			return fmt.Errorf("%s: run mandatory SkillSpector scan: %w", command, err)
		}
	}

	aggregate := buildSkillScanOutput(command, selection, results, runDir, baselineDir, "", false, "json", false, runtimeInfo.Metadata)
	outputPath := filepath.Join(runDir, "mandatory-scan-aggregate.json")
	if err := writeSkillScanOutput("json", outputPath, aggregate, nil); err != nil {
		return fmt.Errorf("%s: write mandatory SkillSpector aggregate output: %w", command, err)
	}

	if !aggregate.Summary.Pass {
		return fmt.Errorf("%s: mandatory SkillSpector scan failed for %d skill(s) with %d unsuppressed issue(s); review %s", command, aggregate.Summary.SkillsScanned, aggregate.Summary.IssueCount, outputPath)
	}
	return nil
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

func resolveAutomaticBaselineDir(sourceRoot string) (string, error) {
	if strings.TrimSpace(sourceRoot) == "" {
		return "", nil
	}
	policyRoot := filepath.Clean(sourceRoot)
	switch filepath.Base(policyRoot) {
	case ".agents", ".claude", ".gemini", ".antigravity", ".opencode", ".hermes":
		policyRoot = filepath.Dir(policyRoot)
	}
	for _, candidate := range []string{
		filepath.Join(policyRoot, ".dotpack", "skillspector", "baselines"),
		filepath.Join(policyRoot, ".agents", "tools", "skillspector-gate", "baselines"),
	} {
		info, err := os.Stat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("stat %s: %w", candidate, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s exists but is not a directory", candidate)
		}
		return candidate, nil
	}
	return "", nil
}

func sanitizeSkillGateCommand(command string) string {
	var b strings.Builder
	for _, r := range command {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "skill-gate"
	}
	return out
}
