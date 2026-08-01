package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate"
	"github.com/ellarock-software/dotpack/internal/skillgate/registry"
	"github.com/ellarock-software/dotpack/internal/skillspector"
)

// spectorGateName is the registry key for the incumbent gate.
const spectorGateName = "skillspector"

// spectorGate adapts the pre-registry SkillSpector enforcement path to
// the Gate interface.
//
// THIS IS A SHIM, and ADR-0016 records it as debt. A proper gate would
// live in its own package under internal/skillgate, but the
// implementation it calls -- runSkillScansWithOptionalBaselines,
// buildSkillScanOutput, writeSkillScanOutput and the ten result structs
// in skills_scan.go -- is shared with the scan-skills and
// baseline-skills commands. Extracting roughly 450 lines out of the
// repository's most test-covered path, as part of a change that already
// introduces a new security gate, is the wrong risk to take at once.
//
// The extension point still holds: a third gate needs no edit to CLI
// core. Only this one gate registers from here rather than from its own
// package.
type spectorGate struct {
	dirs dirs.Dirs
}

func init() {
	registry.Register(spectorGateName, func(d dirs.Dirs) skillgate.Gate {
		return spectorGate{dirs: d}
	})
}

func (spectorGate) Name() string { return spectorGateName }

// Run implements skillgate.Gate.
//
// The context is accepted but not honoured: neither the SkillSpector
// runtime nor its scan invocation is context-aware, and internal/
// skillspector has no timeout anywhere. Threading a deadline through it
// is follow-up work, tracked with the extraction above; the new gate's
// detector runtime already has one.
func (g spectorGate) Run(_ context.Context, req skillgate.Request) (skillgate.Verdict, error) {
	verdict := skillgate.Verdict{Gate: spectorGateName, Pass: true}

	runDir, err := skillgate.RunDir(g.dirs.DotpackHome, spectorGateName, req.Command)
	if err != nil {
		return skillgate.Verdict{}, err
	}

	// An untrusted policy root supplies no suppressions. Previously this
	// gate resolved its baseline directory straight from the source root,
	// so a fetched `github:` repository could ship its own suppressions
	// and silence findings about itself.
	baselineDir := ""
	if req.PolicyRootTrusted {
		baselineDir, err = spectorBaselineDir(req.PolicyRoot)
		if err != nil {
			return skillgate.Verdict{}, err
		}
	}

	var (
		runtimeInfo skillspector.Runtime
		results     []skillScanResult
	)
	if len(req.Selection.Targets) > 0 {
		runtimeInfo, err = ensureSkillSpectorRuntime(g.dirs.DotpackHome)
		if err != nil {
			return skillgate.Verdict{}, fmt.Errorf("ensure SkillSpector runtime: %w", err)
		}
		results, _, err = runSkillScansWithOptionalBaselines(req.Selection.Targets, runDir, baselineDir, "json", runtimeInfo)
		if err != nil {
			return skillgate.Verdict{}, fmt.Errorf("run mandatory SkillSpector scan: %w", err)
		}
	}

	aggregate := buildSkillScanOutput(req.Command, req.Selection, results, runDir, baselineDir, "", false, "json", false, runtimeInfo.Metadata)
	outputPath := filepath.Join(runDir, "mandatory-scan-aggregate.json")
	if err := writeSkillScanOutput("json", outputPath, aggregate, nil); err != nil {
		return skillgate.Verdict{}, fmt.Errorf("write mandatory SkillSpector aggregate output: %w", err)
	}
	verdict.ArtifactPath = outputPath

	if !aggregate.Summary.Pass {
		verdict.Pass = false
		verdict.Reason = fmt.Sprintf("mandatory SkillSpector scan failed for %d skill(s) with %d unsuppressed issue(s)",
			aggregate.Summary.SkillsScanned, aggregate.Summary.IssueCount)
		for _, failing := range aggregate.Summary.FailingSkills {
			verdict.Blocked = append(verdict.Blocked, skillgate.Blocked{
				Skill:  failing.Name,
				Reason: failing.Recommendation,
				Detail: failing.Issues,
			})
		}
	}
	return verdict, nil
}

// spectorBaselineDir finds this gate's reviewed baseline directory under
// a policy root, preserving the original discovery order.
func spectorBaselineDir(policyRoot string) (string, error) {
	if policyRoot == "" {
		return "", nil
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
