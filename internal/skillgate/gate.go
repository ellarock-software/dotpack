// Package skillgate is the contract shared by every skill security gate
// (ADR-0016). It defines the Gate interface the mandatory install-time
// enforcement funnel dispatches through, plus the selection types that
// describe which skill packages a run covers.
//
// Import-cycle safety: this package imports ONLY {dirs} — a strict
// subset of what every concrete gate imports — so any gate
// sub-package can import it without a cycle. It MUST NOT import a
// concrete gate; the concrete packages depend on it, not the reverse.
// The blank-import aggregation lives in internal/skillgate/all.
//
// The selection types live here rather than in internal/cli because the
// registry and the gates both need them and neither may import CLI core.
// internal/cli keeps its original spellings as type aliases, so the ~200
// existing references and the package-wide test stub in
// internal/cli/testmain_test.go compile unchanged.
package skillgate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Selection is the set of skill packages a gate run covers. Targets are
// scanned; SecurityBypassed were removed by an explicit, reported
// --skill-bypass-security and are carried only so a gate can record
// them in its run artifact.
type Selection struct {
	SourceRoot       string
	SkillRoot        string
	Targets          []Target
	SecurityBypassed []Target
}

// Target is one skill package: the directory the gate inspects and the
// SKILL.md that identifies it.
type Target struct {
	Name         string
	SkillDir     string
	SkillFile    string
	RelativePath string
}

// Request is everything a gate is given. It is deliberately host-,
// source- and command-neutral: no field names a concrete gate or leaks
// one implementation's storage layout into the other's.
type Request struct {
	// Command is the dotpack verb that triggered the gate, already
	// sanitized for use in a path segment ("install", "install-all",
	// "import", "inventory", "sync-back").
	Command   string
	Selection Selection

	// Machine-local state roots are deliberately NOT carried here. Every
	// gate is constructed by the registry from a dirs.Dirs, so a gate
	// already holds them; carrying a second copy on the request meant two
	// sources of truth that could disagree, and they did.

	// PolicyRoot is the repository root that owns .dotpack/ policy for
	// this run — approvals, baselines, suppressions. Empty when none
	// could be resolved. It is resolved ONCE by the enforcement funnel so
	// every gate agrees on it; gates derive their own subpaths from it.
	PolicyRoot string

	// PolicyRootTrusted is false when PolicyRoot was derived from a
	// source dotpack itself fetched (a github: or https:// source cloned
	// into DotpackHome). An untrusted policy root MUST NOT supply
	// approvals or suppressions: honouring it would let a remote
	// repository ship its own gate exemptions and approve itself.
	PolicyRootTrusted bool
}

// Blocked is one package a gate refused, with human-readable detail
// lines the CLI prints beneath the summary.
type Blocked struct {
	Skill  string
	Reason string
	Detail []string
}

// Verdict is a gate's decision.
//
// Contract: a POLICY refusal is Verdict{Pass: false}, never an error. An
// error from Run means the gate could not run at all — it could not
// provision its detector, or could not create its run directory. Both
// block the caller; they differ only in the message the CLI prints and
// in whether the operator should read the run artifact or fix their
// machine.
type Verdict struct {
	Gate string
	Pass bool

	// Reason is a one-line summary shown when Pass is false.
	Reason string

	// ArtifactPath is the run artifact the operator should read. Named in
	// the CLI error so a refusal is always reviewable.
	ArtifactPath string

	// Notices are advisory lines printed on a PASSING run: drift, a
	// re-approved baseline, a below-floor finding. Without them the gate's
	// claim that any change becomes a review event would hold only inside
	// a JSON file the operator is never told about.
	Notices []string

	Blocked []Blocked
}

// Gate enforces skill security before dotpack reads or materializes any
// skill package. Implementations self-register with
// internal/skillgate/registry from an init().
type Gate interface {
	// Name is the registry key and the value accepted by --skill-gate.
	Name() string

	// Run enforces the gate over req.Selection. See Verdict for the
	// error-versus-refusal contract.
	Run(ctx context.Context, req Request) (Verdict, error)
}

// Severity ordering, lowest first. A severity outside this list is
// unknown and never blocks: an unrecognised label must not be silently
// promoted into a blocking finding, and the detector's own enum includes
// values ("SAFE") that are explicitly not findings.
var severityOrder = []string{"INFO", "LOW", "MEDIUM", "HIGH", "CRITICAL"}

// SeverityAtLeast reports whether have is at or above floor. Comparison
// is case-insensitive. An unknown value on either side returns false.
func SeverityAtLeast(have, floor string) bool {
	h := severityRank(have)
	f := severityRank(floor)
	if h < 0 || f < 0 {
		return false
	}
	return h >= f
}

func severityRank(s string) int {
	up := strings.ToUpper(strings.TrimSpace(s))
	for i, known := range severityOrder {
		if known == up {
			return i
		}
	}
	return -1
}

// SanitizeCommand reduces a dotpack verb to a safe path segment. Any
// character outside [A-Za-z0-9_-] becomes '-'; an empty result falls
// back to "skill-gate" so a run directory is always creatable.
func SanitizeCommand(command string) string {
	var b strings.Builder
	for _, r := range command {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
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

// TimestampSlug formats now for use in a run-directory name.
func TimestampSlug(now time.Time) string {
	return now.UTC().Format("20060102T150405Z")
}

// RunDir creates and returns a timestamped run directory for one gate
// invocation, at <dotpackHome>/<gateName>/runs/<timestamp>-<command>.
// Each gate owns a root named for itself so run artifacts never collide.
func RunDir(dotpackHome, gateName, command string) (string, error) {
	root := filepath.Join(dotpackHome, gateName, "runs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create %s run directory %s: %w", gateName, root, err)
	}
	dir := filepath.Join(root, TimestampSlug(time.Now())+"-"+SanitizeCommand(command))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s run directory %s: %w", gateName, dir, err)
	}
	return dir, nil
}
