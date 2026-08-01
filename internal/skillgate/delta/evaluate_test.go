package delta

import (
	"fmt"
	"testing"
	"time"
)

const pkgRoot = "/pkg"

func eval(t *testing.T, b *Baseline, findings []Finding, h HashInfo) Evaluation {
	t.Helper()
	return evaluate("demo", pkgRoot, b, withOccurrences(findings, pkgRoot), h, "HIGH")
}

func baselineOf(t *testing.T, findings []Finding, h HashInfo) *Baseline {
	t.Helper()
	e := eval(t, nil, findings, h)
	b := newBaseline("demo", e, "cisco-ai-skill-scanner 2.0.12", "test", "", time.Unix(0, 0))
	return &b
}

// Nothing about an unreviewed package is trusted.
func TestFirstSightingIsBlocked(t *testing.T) {
	e := eval(t, nil, []Finding{{RuleID: "R1", Severity: "LOW", File: "a.py"}}, HashInfo{Digest: "d1"})
	if !e.Blocked() {
		t.Fatalf("first sighting was not blocked: %+v", e)
	}
	if e.Reason == "" {
		t.Error("blocked with no reason")
	}
}

func TestUnchangedPackageIsApproved(t *testing.T) {
	findings := []Finding{{RuleID: "R1", Severity: "HIGH", File: "a.py", Why: "w"}}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, findings, h)

	e := eval(t, b, findings, h)
	if e.Decision != DecisionApproved {
		t.Fatalf("decision = %s (%s), want APPROVED", e.Decision, e.Reason)
	}
}

// The whole point of the gate: a large, constant noise floor is
// baselined once and stops mattering. Measured at ~5% precision, this is
// what makes the detector usable instead of forcing a whole-package
// bypass.
func TestALargeBaselinedNoiseFloorNeverBlocks(t *testing.T) {
	var findings []Finding
	for i := 0; i < 101; i++ {
		findings = append(findings, Finding{
			RuleID: fmt.Sprintf("RULE_%d", i), Severity: "CRITICAL",
			File: fmt.Sprintf("f%d.py", i), Why: fmt.Sprintf("evidence %d", i),
		})
	}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, findings, h)

	e := eval(t, b, findings, h)
	if e.Decision != DecisionApproved {
		t.Fatalf("101 baselined CRITICALs blocked: %s (%s)", e.Decision, e.Reason)
	}
	if len(e.Blocking) != 0 {
		t.Errorf("blocking = %d, want 0", len(e.Blocking))
	}
}

func TestANewFindingAtOrAboveTheFloorBlocks(t *testing.T) {
	base := []Finding{{RuleID: "R1", Severity: "LOW", File: "a.py", Why: "w"}}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, base, h)

	withNew := append(append([]Finding(nil), base...),
		Finding{RuleID: "DATA_EXFIL", Severity: "CRITICAL", File: "b.py", Why: "webhook"})

	e := eval(t, b, withNew, HashInfo{Digest: "d2"})
	if !e.Blocked() {
		t.Fatalf("a new CRITICAL did not block: %+v", e)
	}
	if len(e.Blocking) != 1 || e.Blocking[0].RuleID != "DATA_EXFIL" {
		t.Errorf("blocking = %+v, want the new DATA_EXFIL", e.Blocking)
	}
}

// A new finding below the floor is reported, not blocked -- otherwise
// every INFO the detector invents becomes an outage.
func TestANewFindingBelowTheFloorDoesNotBlock(t *testing.T) {
	base := []Finding{{RuleID: "R1", Severity: "LOW", File: "a.py", Why: "w"}}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, base, h)

	withNew := append(append([]Finding(nil), base...),
		Finding{RuleID: "STYLE", Severity: "LOW", File: "b.py", Why: "nit"})

	e := eval(t, b, withNew, HashInfo{Digest: "d2"})
	if e.Decision != DecisionApprovedWithDrift {
		t.Fatalf("decision = %s (%s), want APPROVED_WITH_DRIFT", e.Decision, e.Reason)
	}
	if len(e.NewFindings) != 1 {
		t.Errorf("new findings = %d, want the LOW reported", len(e.NewFindings))
	}
	if len(e.Blocking) != 0 {
		t.Errorf("a below-floor finding blocked")
	}
}

// Content drift alone is not a security event. Blocking on it would make
// every whitespace edit a gate failure.
func TestContentDriftAloneIsReportedNotBlocked(t *testing.T) {
	findings := []Finding{{RuleID: "R1", Severity: "HIGH", File: "a.py", Why: "w"}}
	b := baselineOf(t, findings, HashInfo{Digest: "d1"})

	e := eval(t, b, findings, HashInfo{Digest: "d2"})
	if e.Decision != DecisionApprovedWithDrift {
		t.Fatalf("decision = %s (%s), want APPROVED_WITH_DRIFT", e.Decision, e.Reason)
	}
	if e.PreviousSHA256 != "d1" || e.ContentSHA256 != "d2" {
		t.Errorf("both digests should be reported for review: %s -> %s", e.PreviousSHA256, e.ContentSHA256)
	}
}

// An approved finding that disappears is not an event either. The gate
// asks "what is new", not "what changed".
func TestAnApprovedFindingDisappearingIsNotAnEvent(t *testing.T) {
	base := []Finding{
		{RuleID: "R1", Severity: "HIGH", File: "a.py", Why: "w1"},
		{RuleID: "R2", Severity: "HIGH", File: "b.py", Why: "w2"},
	}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, base, h)

	e := eval(t, b, base[:1], h)
	if e.Blocked() {
		t.Fatalf("a finding being fixed blocked the install: %+v", e)
	}
}

func TestUnknownSeverityNeverBlocks(t *testing.T) {
	base := []Finding{{RuleID: "R1", Severity: "LOW", File: "a.py", Why: "w"}}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, base, h)

	withNew := append(append([]Finding(nil), base...),
		Finding{RuleID: "R2", Severity: "SAFE", File: "b.py", Why: "not a finding"})

	if e := eval(t, b, withNew, h); e.Blocked() {
		t.Fatalf("an unknown severity blocked: %+v", e.Blocking)
	}
}

// A detector or policy pin bump must not fail the whole estate at once.
// The practical response to a fleet-wide outage is a blanket bypass,
// which is the permanent hole this gate exists to avoid.
func TestProvenanceSkewWarnsRatherThanBlocks(t *testing.T) {
	findings := []Finding{{RuleID: "R1", Severity: "HIGH", File: "a.py", Why: "w"}}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, findings, h)
	b.PolicyVersion = "0"

	e := eval(t, b, findings, h)
	if e.Blocked() {
		t.Fatalf("policy skew blocked the install: %+v", e)
	}
	if len(e.Warnings) == 0 {
		t.Error("policy skew produced no warning")
	}
	if e.Decision != DecisionApprovedWithDrift {
		t.Errorf("decision = %s, want the skew surfaced as drift", e.Decision)
	}
}

func TestABaselineWithoutProvenanceIsFlagged(t *testing.T) {
	b := &Baseline{Skill: "demo", ContentSHA256: "d1", PolicyVersion: policy.PolicyVersion}
	e := eval(t, b, nil, HashInfo{Digest: "d1"})
	if len(e.Warnings) == 0 {
		t.Fatal("a baseline with no detector_version produced no warning")
	}
}

// ------------------------------------------------------------- baseline

func TestNewBaselineRecordsProvenanceAndSortedFingerprints(t *testing.T) {
	findings := []Finding{
		{RuleID: "Z", Severity: "HIGH", File: "z.py", Why: "wz"},
		{RuleID: "A", Severity: "LOW", File: "a.py", Why: "wa"},
	}
	h := HashInfo{Digest: "d1", HashedFiles: 4, RuntimeFilesSkipped: 2}
	e := eval(t, nil, findings, h)

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	b := newBaseline("demo", e, "cisco-ai-skill-scanner 2.0.12", "0.2.0", "reviewed", now)

	if b.ContentSHA256 != "d1" || b.HashedFiles != 4 || b.RuntimeFilesExcludedFromHash != 2 {
		t.Errorf("hash metadata not carried: %+v", b)
	}
	if len(b.ApprovedFindings) != 2 {
		t.Fatalf("approved findings = %d, want 2", len(b.ApprovedFindings))
	}
	if b.ApprovedFindings[0] > b.ApprovedFindings[1] {
		t.Errorf("approved findings are not sorted: %v", b.ApprovedFindings)
	}
	if len(b.FindingDetail) != 2 {
		t.Errorf("finding detail = %d entries, want 2 for the diff reviewer", len(b.FindingDetail))
	}
	if b.DetectorVersion == "" || b.PolicyVersion == "" || b.ToolVersion == "" {
		t.Errorf("provenance incomplete: %+v", b)
	}
	if b.ApprovedAt != "2026-08-01T12:00:00Z" {
		t.Errorf("approved_at = %q", b.ApprovedAt)
	}
	if b.ApprovedReason != "reviewed" {
		t.Errorf("approved_reason = %q", b.ApprovedReason)
	}
}

func TestApprovingThenEvaluatingIsStable(t *testing.T) {
	findings := []Finding{
		{RuleID: "R1", Severity: "CRITICAL", File: "a.py", Why: "w1"},
		{RuleID: "R1", Severity: "CRITICAL", File: "a.py", Why: "w1"},
		{RuleID: "R2", Severity: "INFO", File: "b.py", Why: "w2"},
	}
	h := HashInfo{Digest: "d1"}
	b := baselineOf(t, findings, h)

	if e := eval(t, b, findings, h); e.Decision != DecisionApproved {
		t.Fatalf("a package did not verify clean against its own baseline: %s (%s) new=%+v", e.Decision, e.Reason, e.NewFindings)
	}
}

func TestBaselinePathIsEmptyWithoutAPolicyRoot(t *testing.T) {
	if got := BaselinePath("", "demo"); got != "" {
		t.Errorf("BaselinePath with no policy root = %q, want empty: there is nowhere legitimate to read approvals from", got)
	}
}
