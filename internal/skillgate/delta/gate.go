package delta

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillgate"
	"github.com/ellarock-software/dotpack/internal/skillgate/registry"
	"github.com/ellarock-software/dotpack/internal/skillscanner"
)

// Name is the registry key and the --skill-gate value.
const Name = "skillgate"

func init() {
	registry.Register(Name, func(d dirs.Dirs) skillgate.Gate { return New(d) })
}

// Gate is the delta skill security gate.
type Gate struct {
	dirs dirs.Dirs

	// ToolVersion is stamped into baselines this gate writes. Set by the
	// CLI so an approval records which dotpack made it.
	ToolVersion string
}

// New constructs the gate.
func New(d dirs.Dirs) *Gate { return &Gate{dirs: d} }

// ensureRuntime is the provisioning seam. Tests replace it so that no
// test path ever reaches a real pip install; internal/skillscanner also
// refuses to provision when DOTPACK_SKILLGATE_NO_PROVISION is set, so a
// test that forgets fails loudly instead of silently hitting PyPI.
var ensureRuntime = skillscanner.EnsureRuntime

// Name implements skillgate.Gate.
func (g *Gate) Name() string { return Name }

// PackageResult is one package's outcome, for the run artifact.
type PackageResult struct {
	Skill      string     `json:"skill"`
	Path       string     `json:"path"`
	Decision   Decision   `json:"decision"`
	Reason     string     `json:"reason"`
	Evaluation Evaluation `json:"evaluation"`

	// BaselinePath is where the approval was read from, or where it must
	// be written. Named in output so the operator can go look at it.
	BaselinePath string `json:"baseline_path,omitempty"`

	// PolicyRoot is the repository that owns the approval. Carried
	// explicitly rather than recovered from BaselinePath, so the
	// baseline layout can change without silently mis-keying the
	// machine-local seen records.
	PolicyRoot string `json:"policy_root,omitempty"`

	// BaselineChangedSinceLastInstall is the machine-local tripwire: the
	// committed approval is not the one this machine last installed
	// against. Advisory, never blocking.
	BaselineChangedSinceLastInstall bool `json:"baseline_changed_since_last_install,omitempty"`

	UnsafeLinks []unsafeLink `json:"unsafe_links,omitempty"`
}

type runArtifact struct {
	GeneratedAt     string          `json:"generated_at"`
	Gate            string          `json:"gate"`
	Command         string          `json:"command"`
	PolicyRoot      string          `json:"policy_root"`
	PolicyTrusted   bool            `json:"policy_root_trusted"`
	PolicyVersion   string          `json:"policy_version"`
	DetectorVersion string          `json:"detector_version"`
	FailOn          string          `json:"fail_on"`
	Pass            bool            `json:"pass"`
	Results         []PackageResult `json:"results"`
	Bypassed        []string        `json:"security_bypassed_skills,omitempty"`
}

// Run implements skillgate.Gate.
func (g *Gate) Run(ctx context.Context, req skillgate.Request) (skillgate.Verdict, error) {
	verdict := skillgate.Verdict{Gate: Name, Pass: true}

	runDir, err := skillgate.RunDir(g.dirs.DotpackHome, Name, req.Command)
	if err != nil {
		return skillgate.Verdict{}, err
	}

	// An untrusted policy root supplies no approvals. dotpack clones a
	// github: source into DotpackHome and then treats the clone as the
	// source root, so honouring approvals from there would let a remote
	// repository ship its own exemptions and approve itself. With no
	// approvals available every package is a first sighting, which is
	// exactly the intended outcome: a fetched skill must be reviewed
	// against the installing repository's own baselines.
	policyRoot := req.PolicyRoot
	if !req.PolicyRootTrusted {
		policyRoot = ""
	}

	var runtimeInfo skillscanner.Runtime
	if len(req.Selection.Targets) > 0 {
		runtimeInfo, err = ensureRuntime(ctx, g.dirs.DotpackHome)
		if err != nil {
			return skillgate.Verdict{}, err
		}
	}

	artifact := runArtifact{
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Gate:            Name,
		Command:         req.Command,
		PolicyRoot:      policyRoot,
		PolicyTrusted:   req.PolicyRootTrusted,
		PolicyVersion:   policy.PolicyVersion,
		DetectorVersion: runtimeInfo.Metadata.DetectorVersion(),
		FailOn:          policy.FailOnSeverity,
		Pass:            true,
	}
	for _, b := range req.Selection.SecurityBypassed {
		artifact.Bypassed = append(artifact.Bypassed, b.Name)
	}

	for _, target := range req.Selection.Targets {
		result := g.inspect(ctx, target, policyRoot, runtimeInfo)
		artifact.Results = append(artifact.Results, result)
		if result.Decision == DecisionBlocked {
			artifact.Pass = false
			verdict.Pass = false
			verdict.Blocked = append(verdict.Blocked, skillgate.Blocked{
				Skill:  target.Name,
				Reason: result.Reason,
				Detail: blockedDetail(result),
			})
		}
	}

	artifactPath := filepath.Join(runDir, "skillgate-aggregate.json")
	if err := writeArtifact(artifactPath, artifact); err != nil {
		return skillgate.Verdict{}, err
	}
	verdict.ArtifactPath = artifactPath

	if !verdict.Pass {
		verdict.Reason = fmt.Sprintf("%d of %d skill package(s) refused by the delta gate",
			len(verdict.Blocked), len(req.Selection.Targets))
	}
	return verdict, nil
}

// Inspect evaluates one package without writing a run artifact. It is
// the entry point approve-skill uses.
func (g *Gate) Inspect(ctx context.Context, target skillgate.Target, policyRoot string, rt skillscanner.Runtime) PackageResult {
	return g.inspect(ctx, target, policyRoot, rt)
}

// EnsureRuntime provisions the detector.
func (g *Gate) EnsureRuntime(ctx context.Context) (skillscanner.Runtime, error) {
	return ensureRuntime(ctx, g.dirs.DotpackHome)
}

// Approve writes a baseline for a package at its current state.
func (g *Gate) Approve(result PackageResult, detectorVersion, reason string) error {
	if result.BaselinePath == "" {
		return fmt.Errorf("approve %s: no policy root to write the approval into", result.Skill)
	}
	if result.Evaluation.Hash.HashedFiles == 0 {
		return fmt.Errorf("approve %s: %s", result.Skill, result.Reason)
	}
	b := newBaseline(result.Skill, result.Evaluation, detectorVersion, g.ToolVersion, reason, time.Now())
	if err := writeBaseline(result.BaselinePath, b); err != nil {
		return err
	}
	return g.recordSeen(result)
}

// RecordSeen notes that this machine installed the package at its
// current approved state.
func (g *Gate) RecordSeen(result PackageResult) error { return g.recordSeen(result) }

func (g *Gate) recordSeen(result PackageResult) error {
	if result.BaselinePath == "" || result.PolicyRoot == "" {
		return nil
	}
	return writeSeen(g.dirs.DotpackHome, result.PolicyRoot, SeenRecord{
		Skill:          result.Skill,
		PolicyRoot:     result.PolicyRoot,
		BaselineSHA256: fileSHA256(result.BaselinePath),
		ContentSHA256:  result.Evaluation.ContentSHA256,
		SeenAt:         time.Now().UTC().Format(time.RFC3339),
	})
}

// inspect runs the ordered gates over one package. Each stage fails
// closed; the first failure short-circuits, so a package that cannot be
// read is never partially evaluated.
func (g *Gate) inspect(ctx context.Context, target skillgate.Target, policyRoot string, rt skillscanner.Runtime) PackageResult {
	pkgAbs, err := filepath.Abs(target.SkillDir)
	if err != nil {
		return blocked(target, fmt.Sprintf("cannot resolve package path: %v", err))
	}
	pkgAbs = filepath.Clean(pkgAbs)

	result := PackageResult{
		Skill:        target.Name,
		Path:         pkgAbs,
		PolicyRoot:   policyRoot,
		BaselinePath: BaselinePath(policyRoot, target.Name),
	}

	// 1. SKILL.md must exist and must be a real file. Checked with Lstat
	// rather than Stat, so a symlinked SKILL.md is caught here instead of
	// passing an existence test and then being hashed by nothing.
	info, err := os.Lstat(filepath.Join(pkgAbs, "SKILL.md"))
	if err != nil || !(info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return withBase(result, DecisionBlocked, "no SKILL.md - not an installable skill package")
	}

	// 2. Symlinks.
	walked, err := walkPackage(pkgAbs)
	if err != nil {
		return withBase(result, DecisionBlocked, fmt.Sprintf("package could not be fully read: %v", err))
	}
	if unsafe := findSymlinks(pkgAbs, walked.Links); len(unsafe) > 0 {
		result.UnsafeLinks = unsafe
		return withBase(result, DecisionBlocked, fmt.Sprintf("%d symlink(s) in the package", len(unsafe)))
	}

	// 3. Content hash.
	hashInfo := packageHash(pkgAbs, walked.Files, policy)
	if hashInfo.HashedFiles == 0 {
		return withBase(result, DecisionBlocked, "0 files were hashed - nothing was actually inspected")
	}

	// 4. Detector.
	det := scanDetector(ctx, rt.ScannerBin, pkgAbs, skillscanner.DefaultScanTimeout)
	if det.Err != "" {
		return withBase(result, DecisionBlocked, det.Err)
	}

	// 5. Findings, in the canonical order: detector first, invisible
	// second. withOccurrences assigns indices in this order, so changing
	// it renumbers findings and invalidates every approved fingerprint.
	findings := append(det.Findings, scanInvisible(pkgAbs, walked.Files, policy)...)
	findings = withOccurrences(findings, pkgAbs)

	baseline, err := loadBaseline(result.BaselinePath)
	if err != nil {
		return withBase(result, DecisionBlocked, err.Error())
	}

	result.Evaluation = evaluate(target.Name, pkgAbs, baseline, findings, hashInfo, policy.FailOnSeverity)
	result.Decision = result.Evaluation.Decision
	result.Reason = result.Evaluation.Reason

	if baseline != nil {
		result.BaselineChangedSinceLastInstall = g.baselineChanged(policyRoot, target.Name, result.BaselinePath)
	}
	return result
}

// baselineChanged reports whether the committed approval differs from
// the one this machine last installed against. Advisory only: a changed
// approval is a thing to look at, not in itself a refusal.
func (g *Gate) baselineChanged(policyRoot, skill, baselinePath string) bool {
	seen, _ := loadSeen(g.dirs.DotpackHome, policyRoot, skill)
	if seen == nil || seen.BaselineSHA256 == "" {
		return false
	}
	return seen.BaselineSHA256 != fileSHA256(baselinePath)
}

func blocked(target skillgate.Target, reason string) PackageResult {
	return PackageResult{Skill: target.Name, Path: target.SkillDir, Decision: DecisionBlocked, Reason: reason}
}

func withBase(r PackageResult, d Decision, reason string) PackageResult {
	r.Decision = d
	r.Reason = reason
	r.Evaluation.Decision = d
	r.Evaluation.Reason = reason
	return r
}

// blockedDetail renders the operator-facing lines beneath a refusal. The
// message must name the remedy: without it the first reaction to a
// first-sighting block is a blanket bypass, which is the permanent hole
// this gate exists to avoid.
func blockedDetail(r PackageResult) []string {
	var out []string
	for _, l := range r.UnsafeLinks {
		out = append(out, fmt.Sprintf("%s -> %s", l.File, l.Target))
	}
	if len(r.UnsafeLinks) > 0 {
		out = append(out,
			"Symlinked content is hashed by nothing and scanned by nothing, so it",
			"could be swapped after approval. Replace them with regular files.")
		return out
	}

	for _, f := range r.Evaluation.Blocking {
		loc := f.File
		if f.Line != nil {
			loc = fmt.Sprintf("%s:%d", f.File, *f.Line)
		} else if len(f.Lines) > 0 {
			loc = fmt.Sprintf("%s:%v", f.File, f.Lines)
		}
		out = append(out, fmt.Sprintf("[%s] %s  %s", f.Severity, f.RuleID, loc))
		if f.Title != "" {
			out = append(out, "         "+f.Title)
		}
	}
	if r.Evaluation.Decision == DecisionBlocked && len(r.Evaluation.Blocking) == 0 && len(r.UnsafeLinks) == 0 {
		if r.BaselinePath != "" {
			out = append(out, "Review the package, then approve it:",
				fmt.Sprintf("    dotpack approve-skill --skill %s", r.Skill))
		}
	} else if len(r.Evaluation.Blocking) > 0 && r.BaselinePath != "" {
		out = append(out, "If these are expected, review them and re-approve:",
			fmt.Sprintf("    dotpack approve-skill --skill %s", r.Skill))
	}
	return out
}

func writeArtifact(path string, a runArtifact) error {
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("encode skillgate run artifact: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write skillgate run artifact %s: %w", path, err)
	}
	return nil
}
