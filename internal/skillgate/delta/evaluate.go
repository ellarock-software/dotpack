package delta

import (
	"fmt"

	"github.com/ellarock-software/dotpack/internal/skillgate"
)

// Decision is the outcome of one package evaluation.
type Decision string

const (
	// DecisionApproved means the package is byte-identical to its
	// approved state.
	DecisionApproved Decision = "APPROVED"

	// DecisionApprovedWithDrift means durable content changed but
	// introduced no new finding at or above the floor. The change is
	// reported, not blocked: content drift alone is not a security event,
	// and blocking on it would make every whitespace edit a gate failure.
	DecisionApprovedWithDrift Decision = "APPROVED_WITH_DRIFT"

	// DecisionBlocked means the package is refused.
	DecisionBlocked Decision = "BLOCKED"
)

// Evaluation is the full result for one package.
type Evaluation struct {
	Skill    string   `json:"skill"`
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason"`

	ContentSHA256  string `json:"content_sha256"`
	PreviousSHA256 string `json:"previous_sha256,omitempty"`

	TotalFindings int       `json:"total_findings"`
	NewFindings   []Finding `json:"new_findings,omitempty"`
	Blocking      []Finding `json:"blocking_findings,omitempty"`

	FailOn string   `json:"fail_on"`
	Hash   HashInfo `json:"hash"`

	// Warnings surface conditions that are reported but do not block,
	// such as provenance skew between the baseline and the current
	// detector or policy.
	Warnings []string `json:"warnings,omitempty"`

	// fingerprints is the ordered fingerprint list for the current
	// findings, used when writing a baseline.
	fingerprints []string
	current      map[string]Finding
}

// Blocked reports whether the evaluation refuses the package.
func (e Evaluation) Blocked() bool { return e.Decision == DecisionBlocked }

// evaluate compares the current findings against an approved baseline.
//
// The delta rule, and the whole reason this gate exists: an approved
// fingerprint that is still present is not an event, and an approved
// fingerprint that DISAPPEARS is not an event either. Only a fingerprint
// that is new since approval, at or above the severity floor, blocks.
// That is what makes a noisy detector usable -- its constant output is
// baselined once and stops mattering, so precision stops mattering and
// recall starts.
//
// findings must already carry occurrence indices (see withOccurrences)
// and must be in the canonical order: detector findings first,
// invisible-scan findings second.
func evaluate(skill, pkgAbs string, baseline *Baseline, findings []Finding, h HashInfo, failOn string) Evaluation {
	e := Evaluation{
		Skill:         skill,
		ContentSHA256: h.Digest,
		TotalFindings: len(findings),
		FailOn:        failOn,
		Hash:          h,
		current:       make(map[string]Finding, len(findings)),
	}

	for _, f := range findings {
		fp := fingerprint(f, pkgAbs)
		if _, seen := e.current[fp]; !seen {
			e.fingerprints = append(e.fingerprints, fp)
		}
		e.current[fp] = f
	}

	// No baseline: nothing about this package has been reviewed, so
	// nothing about it is trusted. First sighting always blocks.
	//
	// Every finding is reported, not just counted. "Review the package,
	// then approve it" is not actionable if the operator cannot see what
	// the detector found -- and an operator who cannot see it will reach
	// for a bypass instead.
	if baseline == nil {
		e.Decision = DecisionBlocked
		e.Reason = fmt.Sprintf("no approved baseline - first sighting of this package. %d finding(s) present.", len(findings))
		for _, fp := range e.fingerprints {
			f := e.current[fp]
			e.NewFindings = append(e.NewFindings, f)
			if skillgate.SeverityAtLeast(f.Severity, failOn) {
				e.Blocking = append(e.Blocking, f)
			}
		}
		return e
	}

	e.PreviousSHA256 = baseline.ContentSHA256
	e.Warnings = baseline.provenanceWarnings()

	known := newSet(baseline.ApprovedFindings)
	for _, fp := range e.fingerprints {
		if known.has(fp) {
			continue
		}
		f := e.current[fp]
		e.NewFindings = append(e.NewFindings, f)
		if skillgate.SeverityAtLeast(f.Severity, failOn) {
			e.Blocking = append(e.Blocking, f)
		}
	}

	switch {
	case len(e.Blocking) > 0:
		e.Decision = DecisionBlocked
		e.Reason = fmt.Sprintf("%d new finding(s) at or above %s since approval", len(e.Blocking), failOn)
	case baseline.ContentSHA256 != h.Digest:
		e.Decision = DecisionApprovedWithDrift
		e.Reason = fmt.Sprintf("durable content changed but introduced no new finding at or above %s", failOn)
	default:
		e.Decision = DecisionApproved
		e.Reason = "durable content identical to approved baseline"
	}

	// Provenance skew is reported, never blocking. Blocking on a detector
	// or policy pin bump would fail every package in the estate at once,
	// and the practical response to a fleet-wide outage is a blanket
	// bypass -- which is the permanent hole this gate exists to avoid.
	if len(e.Warnings) > 0 && e.Decision == DecisionApproved {
		e.Decision = DecisionApprovedWithDrift
		e.Reason = "approved, with provenance skew since the baseline was written"
	}

	return e
}
