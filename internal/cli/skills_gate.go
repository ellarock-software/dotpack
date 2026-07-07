package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/internal/skillspector"
)

var mandatorySkillScan = runMandatorySkillScan
var ensureSkillSpectorRuntime = skillspector.EnsureRuntime

func runMandatorySkillScan(command string, selection skillScanSelection, d dirs.Dirs) error {
	if len(selection.Targets) == 0 {
		return nil
	}

	runtimeInfo, err := ensureSkillSpectorRuntime(d.DotpackHome)
	if err != nil {
		return fmt.Errorf("%s: ensure SkillSpector runtime: %w", command, err)
	}

	runDir, err := createSkillSpectorRunDir(d.DotpackHome, sanitizeSkillGateCommand(command))
	if err != nil {
		return fmt.Errorf("%s: create SkillSpector run directory: %w", command, err)
	}

	baselineDir, err := resolveAutomaticBaselineDir(selection.SourceRoot)
	if err != nil {
		return fmt.Errorf("%s: resolve automatic baseline directory: %w", command, err)
	}

	results, _, err := runSkillScans(selection.Targets, runDir, baselineDir, "json", runtimeInfo)
	if err != nil {
		return fmt.Errorf("%s: run mandatory SkillSpector scan: %w", command, err)
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

func ensureMandatorySkillScanForSource(command, source string, d dirs.Dirs) error {
	selection, err := resolveSkillScanSelection(source, sourceLayoutOptions{}, nil, false, "HEAD", d)
	if err != nil {
		return err
	}
	return mandatorySkillScan(command, selection, d)
}

func ensureMandatorySkillScanForSourceLayout(command string, layout sourceLayout, d dirs.Dirs) error {
	return ensureMandatorySkillScanForSkillRoot(command, layout.root, layout.kindDir(resource.KindSkill), d)
}

func ensureMandatorySkillScanForSkillRoot(command, sourceRoot, skillRoot string, d dirs.Dirs) error {
	selection, err := buildSkillScanSelection(sourceRoot, skillRoot)
	if err != nil {
		return err
	}
	return mandatorySkillScan(command, selection, d)
}

func ensureMandatorySkillScanForSkillFile(command, skillFile, sourceRoot string, d dirs.Dirs) error {
	selection, err := buildSingleSkillScanSelection(skillFile, sourceRoot)
	if err != nil {
		return err
	}
	return mandatorySkillScan(command, selection, d)
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

func buildSingleSkillScanSelection(skillFile, sourceRoot string) (skillScanSelection, error) {
	if strings.TrimSpace(sourceRoot) == "" {
		sourceRoot = inferSingleSkillSourceRoot(skillFile)
	}
	target, err := buildSkillScanTarget(skillFile, sourceRoot)
	if err != nil {
		return skillScanSelection{}, err
	}
	return skillScanSelection{
		SourceRoot: sourceRoot,
		SkillRoot:  filepath.Dir(skillFile),
		Targets:    []skillScanTarget{target},
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
	candidate := filepath.Join(policyRoot, ".dotpack", "skillspector", "baselines")
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat %s: %w", candidate, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s exists but is not a directory", candidate)
	}
	return candidate, nil
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
