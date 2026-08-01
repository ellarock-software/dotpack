package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Baseline is a package's approved security state.
//
// It is a plain JSON file, committed to the repository that owns the
// package, and the review control is the pull-request diff. That is a
// deliberate trade, stated in ADR-0016: baselines are tamper-EVIDENT,
// not tamper-proof. Anyone who can land a commit can approve a finding,
// exactly as anyone who can land a commit can add the code the finding
// describes. What the provenance fields buy is that an approval cannot
// be silent -- the diff names the detector, the policy and the moment.
type Baseline struct {
	Skill                        string `json:"skill"`
	ContentSHA256                string `json:"content_sha256"`
	HashedFiles                  int    `json:"hashed_files"`
	RuntimeFilesExcludedFromHash int    `json:"runtime_files_excluded_from_hash"`

	// ApprovedFindings is the sorted fingerprint set. This is the noise
	// floor: everything here was reviewed once and never fires again.
	ApprovedFindings []string `json:"approved_findings"`

	// FindingDetail is documentation for whoever reviews the diff. Only
	// ContentSHA256 and ApprovedFindings are load-bearing.
	FindingDetail map[string]Finding `json:"finding_detail"`

	// Provenance. Absent from the source implementation, and its absence
	// is what made an approval unauditable: a fingerprint set alone does
	// not say what produced it.
	DetectorVersion string `json:"detector_version"`
	PolicyVersion   string `json:"policy_version"`
	ToolVersion     string `json:"tool_version"`
	ApprovedAt      string `json:"approved_at"`
	ApprovedReason  string `json:"approved_reason,omitempty"`
}

// provenanceWarnings reports skew between what approved this baseline
// and what is running now. Reported, never blocking; see evaluate.
func (b *Baseline) provenanceWarnings() []string {
	var out []string
	if b.DetectorVersion == "" {
		out = append(out, "baseline records no detector_version; it predates provenance tracking and its fingerprints cannot be attributed to a known detector")
	}
	if b.PolicyVersion != "" && b.PolicyVersion != policy.PolicyVersion {
		out = append(out, fmt.Sprintf("baseline was approved under policy version %s; this gate runs policy version %s", b.PolicyVersion, policy.PolicyVersion))
	}
	return out
}

// DetectorSkew reports whether the baseline was approved under a
// different detector than the one now running.
func (b *Baseline) DetectorSkew(current string) bool {
	return b.DetectorVersion != "" && current != "" && b.DetectorVersion != current
}

// BaselineDir is the directory holding approved baselines for a policy
// root. Empty policyRoot yields an empty result: with no repository to
// own the approvals there is nowhere legitimate to read them from.
func BaselineDir(policyRoot string) string {
	if policyRoot == "" {
		return ""
	}
	return filepath.Join(policyRoot, filepath.FromSlash(policy.BaselineDirectory))
}

// BaselinePath is the baseline file for one skill.
func BaselinePath(policyRoot, skill string) string {
	dir := BaselineDir(policyRoot)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, skill+".json")
}

// loadBaseline reads a baseline. A missing file returns (nil, nil): the
// package is unapproved, which the caller treats as a first sighting. A
// present but unreadable or malformed file is an ERROR, not an absence
// -- silently treating corruption as "unapproved" would be survivable,
// but silently treating it as "approved" would not, and an error keeps
// the two from ever being confused.
func loadBaseline(path string) (*Baseline, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	return &b, nil
}

// writeBaseline persists an approval.
func writeBaseline(path string, b Baseline) error {
	if path == "" {
		return fmt.Errorf("no baseline path: the run has no policy root to write approvals into")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create baseline directory %s: %w", filepath.Dir(path), err)
	}
	raw, err := json.MarshalIndent(b, "", " ")
	if err != nil {
		return fmt.Errorf("encode baseline for %s: %w", b.Skill, err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write baseline %s: %w", path, err)
	}
	return nil
}

// newBaseline builds an approval from an evaluation.
func newBaseline(skill string, e Evaluation, detectorVersion, toolVersion, reason string, now time.Time) Baseline {
	fingerprints := append([]string(nil), e.fingerprints...)
	sort.Strings(fingerprints)

	detail := make(map[string]Finding, len(e.current))
	for fp, f := range e.current {
		detail[fp] = f
	}

	return Baseline{
		Skill:                        skill,
		ContentSHA256:                e.ContentSHA256,
		HashedFiles:                  e.Hash.HashedFiles,
		RuntimeFilesExcludedFromHash: e.Hash.RuntimeFilesSkipped,
		ApprovedFindings:             fingerprints,
		FindingDetail:                detail,
		DetectorVersion:              detectorVersion,
		PolicyVersion:                policy.PolicyVersion,
		ToolVersion:                  toolVersion,
		ApprovedAt:                   now.UTC().Format(time.RFC3339),
		ApprovedReason:               reason,
	}
}

// SeenRecord is a machine-local note that this machine has installed a
// package at a given approved state.
//
// It exists because the committed baseline alone cannot show CHANGE: a
// reviewer sees the diff, but an operator running install sees only the
// current file. Recording what was last installed here lets install warn
// "the approval for this package changed since you last installed it",
// which is the signal that matters when a baseline is edited by someone
// else. It is advisory and never blocks.
type SeenRecord struct {
	Skill          string `json:"skill"`
	PolicyRoot     string `json:"policy_root"`
	BaselineSHA256 string `json:"baseline_sha256"`
	ContentSHA256  string `json:"content_sha256"`
	ApprovedAt     string `json:"approved_at"`
	SeenAt         string `json:"seen_at"`
}

func seenDir(dotpackHome, policyRoot string) string {
	sum := sha256.Sum256([]byte(policyRoot))
	return filepath.Join(dotpackHome, "skillgate", "seen", hex.EncodeToString(sum[:])[:16])
}

func seenPath(dotpackHome, policyRoot, skill string) string {
	if dotpackHome == "" || policyRoot == "" {
		return ""
	}
	return filepath.Join(seenDir(dotpackHome, policyRoot), skill+".json")
}

func loadSeen(dotpackHome, policyRoot, skill string) (*SeenRecord, error) {
	path := seenPath(dotpackHome, policyRoot, skill)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		// A missing or unreadable seen record is not a security signal;
		// it just means this machine has no prior observation.
		return nil, nil //nolint:nilerr // advisory state, never blocking
	}
	var r SeenRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, nil //nolint:nilerr // advisory state, never blocking
	}
	return &r, nil
}

func writeSeen(dotpackHome, policyRoot string, r SeenRecord) error {
	path := seenPath(dotpackHome, policyRoot, r.Skill)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create seen directory: %w", err)
	}
	raw, err := json.MarshalIndent(r, "", " ")
	if err != nil {
		return fmt.Errorf("encode seen record: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write seen record %s: %w", path, err)
	}
	return nil
}

// fileSHA256 hashes a file's bytes, used to detect that a baseline file
// itself changed since this machine last installed the package.
func fileSHA256(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
