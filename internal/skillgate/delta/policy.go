package delta

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// policyJSON is the gate's policy document, compiled in.
//
// It is deliberately NOT readable from the repository being installed.
// hash_exclusions.paths and invisible_character_policy.scan_suffixes
// decide what the content tripwire covers and which files are searched
// for hidden codepoints; a package that could edit either could exclude
// itself from both. This is the same reasoning that keeps gate selection
// operator-controlled. Precedent for an embedded policy document is
// internal/cli/lifecycle_tasks.yaml.
//
//go:embed policy.json
var policyJSON []byte

// Policy is the gate's configuration.
type Policy struct {
	ID            string `json:"id"`
	PolicyVersion string `json:"policy_version"`

	// FailOnSeverity is the floor at or above which a NEW finding blocks.
	FailOnSeverity string `json:"fail_on_severity"`
	FailClosed     bool   `json:"fail_closed"`

	// BaselineDirectory is the repository-relative location of approved
	// baselines, resolved against the run's policy root.
	BaselineDirectory string `json:"baseline_directory"`

	Detector       PolicyDetector       `json:"detector"`
	HashExclusions PolicyHashExclusions `json:"hash_exclusions"`
	Invisible      PolicyInvisible      `json:"invisible_character_policy"`
}

type PolicyDetector struct {
	Name                    string   `json:"name"`
	Package                 string   `json:"package"`
	License                 string   `json:"license"`
	EnginesEnabledByDefault []string `json:"engines_enabled_by_default"`
	NetworkRequired         bool     `json:"network_required"`
}

type PolicyHashExclusions struct {
	// Paths is the allowlist. A package earns an exclusion only by
	// declaring the name in its own .gitignore AND that name appearing
	// here; the package's file may narrow this list, never extend it.
	Paths []string `json:"paths"`

	HonorPackageGitignore bool `json:"honor_package_gitignore"`

	// NestablePaths match at any depth. Everything else is root-anchored,
	// so declaring "logs" does not also exclude "scripts/logs/".
	NestablePaths []string `json:"nestable_paths"`
}

type PolicyInvisible struct {
	Severity     string   `json:"severity"`
	ScanSuffixes []string `json:"scan_suffixes"`
}

// policy is parsed once at package init. A malformed embedded policy is
// a build-time mistake that must fail immediately and loudly: the gate
// cannot be trusted to fail closed if its own configuration is unknown.
var policy = mustParsePolicy(policyJSON)

func mustParsePolicy(raw []byte) Policy {
	p, err := parsePolicy(raw)
	if err != nil {
		panic(fmt.Sprintf("skillgate delta: embedded policy is invalid: %v", err))
	}
	return p
}

func parsePolicy(raw []byte) (Policy, error) {
	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return Policy{}, fmt.Errorf("parse policy: %w", err)
	}
	if p.FailOnSeverity == "" {
		return Policy{}, fmt.Errorf("policy has no fail_on_severity")
	}
	if !p.FailClosed {
		return Policy{}, fmt.Errorf("policy does not declare fail_closed")
	}
	if p.PolicyVersion == "" {
		return Policy{}, fmt.Errorf("policy has no policy_version")
	}
	if p.BaselineDirectory == "" {
		return Policy{}, fmt.Errorf("policy has no baseline_directory")
	}
	if len(p.Invisible.ScanSuffixes) == 0 {
		return Policy{}, fmt.Errorf("policy has no invisible_character_policy.scan_suffixes")
	}
	return p, nil
}

// set is a small string set; the algorithm compares path segments and
// exclusion names constantly and reads better than repeated map literals.
type set map[string]struct{}

func newSet(items []string) set {
	s := make(set, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}

func (s set) has(v string) bool {
	_, ok := s[v]
	return ok
}
