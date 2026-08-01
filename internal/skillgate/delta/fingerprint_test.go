package delta

import (
	"testing"
)

func intp(v int) *int { return &v }

// ------------------------------------------------------------ normalise

// Every expectation here was produced by running the source
// skillgate.mjs normalisePath on the same input. The port must agree
// exactly, because this function's output is a fingerprint input.
func TestNormalisePathMatchesTheSourceImplementation(t *testing.T) {
	cases := []struct {
		value, pkg, want string
		why              string
	}{
		{"/tmp/pkg/scripts/h.py", "/tmp/pkg", "scripts/h.py", "absolute path inside the package is made relative"},
		{"scripts/h.py", "/tmp/pkg", "scripts/h.py", "an already-relative path is untouched"},
		{"", "/tmp/pkg", "", "empty stays empty"},

		// The substring bug: package /s/rr/foo used to eat the prefix
		// inside /s/rr/foobar/module.py, mangling unrelated evidence and
		// collapsing distinct findings onto one fingerprint.
		{"call in /s/rr/foobar/module.py exfiltrates", "/s/rr/foo", "call in /s/rr/foobar/module.py exfiltrates", "a longer sibling path is not a match"},
		{"/s/rr/foobar/module.py", "/s/rr/foo", "s/rr/foobar/module.py", "only the leading slash is trimmed from a sibling"},
		{"/s/rr/foo.py", "/s/rr/foo", "s/rr/foo.py", "a file whose name extends the package name is not a match"},

		// The terminator bug: the anchor originally required a slash,
		// whitespace or end of input, so quoted and punctuated evidence
		// kept the absolute path and leaked the install location into
		// fingerprints.
		{`open("/home/a/pkg")`, "/home/a/pkg", `open("")`, "a quote terminates the path"},
		{"/home/a/pkg:5: warning", "/home/a/pkg", ":5: warning", "a colon terminates the path"},
		{"a, /home/a/pkg, b", "/home/a/pkg", "a, , b", "a comma terminates the path"},
		{"see /home/a/pkg; next", "/home/a/pkg", "see ; next", "a semicolon terminates the path"},
		{"ends at /home/a/pkg", "/home/a/pkg", "ends at ", "end of input terminates the path"},
	}
	for _, tc := range cases {
		if got := normalisePath(tc.value, tc.pkg); got != tc.want {
			t.Errorf("normalisePath(%q, %q) = %q, want %q (%s)", tc.value, tc.pkg, got, tc.want, tc.why)
		}
	}
}

func TestNormalisePathStripsEveryOccurrence(t *testing.T) {
	// Cross-checked against skillgate.mjs, which returns the same string:
	// the leading prose is preserved, both separated occurrences are
	// stripped, and the trailing bare root is stripped at end of input.
	got := normalisePath("from /p/x/a.py to /p/x/b.py via /p/x", "/p/x")
	if want := "from a.py to b.py via "; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCollapseSpaceNormalisesWrapping(t *testing.T) {
	if got, want := collapseSpace("  a\n\tb   c  "), "a b c"; got != want {
		t.Fatalf("collapseSpace = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------- fingerprint

// If the keyed field set or the join order ever changes, every baseline
// in every adopter's repository silently stops matching; this makes that
// a test failure instead.
//
// This deliberately DIFFERS from the reference implementation, which
// keyed on the description alone. See fingerprint() for why that was a
// working bypass.
func TestFingerprintGoldenVector(t *testing.T) {
	f := Finding{
		RuleID:   "DATA_EXFIL_HTTP_POST",
		Severity: "HIGH",
		Analyzer: "static",
		File:     "/tmp/pkg/scripts/send.py",
		Line:     intp(12),
		Title:    "Outbound POST to a non-allowlisted host",
		Why:      "POST to https://discord.com/api/webhooks/xxx in /tmp/pkg/scripts/send.py",
	}
	const want = "d726c5e0e8f36429"
	if got := fingerprint(f, "/tmp/pkg"); got != want {
		t.Fatalf("fingerprint drifted.\n got: %s\nwant: %s\n\nThe keyed field set or its join order changed. Every existing baseline stops matching.", got, want)
	}
}

// Editing prose above a finding must not resurface it as new.
func TestFingerprintIgnoresLineNumbers(t *testing.T) {
	base := Finding{RuleID: "R1", File: "a.py", Title: "T", Why: "W", Line: intp(10)}
	moved := base
	moved.Line = intp(4000)
	if fingerprint(base, "/pkg") != fingerprint(moved, "/pkg") {
		t.Fatal("a line-number change produced a different fingerprint")
	}
}

// A detector re-classification is not a new finding.
func TestFingerprintIgnoresSeverityAndAnalyzer(t *testing.T) {
	base := Finding{RuleID: "R1", File: "a.py", Title: "T", Why: "W", Severity: "LOW", Analyzer: "static"}
	reclassified := base
	reclassified.Severity = "CRITICAL"
	reclassified.Analyzer = "behavioral"
	if fingerprint(base, "/pkg") != fingerprint(reclassified, "/pkg") {
		t.Fatal("a severity or analyzer change produced a different fingerprint")
	}
}

// The collision bug, and the reason evidence is a keyed field: detector
// titles are static, so a second DATA_EXFIL_HTTP_POST in an approved
// file -- one benign, one to an attacker's webhook -- shared a
// fingerprint and the malicious one was silently swallowed.
func TestFingerprintSeparatesSameRuleAndFileByEvidence(t *testing.T) {
	approved := Finding{
		RuleID: "DATA_EXFIL_HTTP_POST", File: "send.py", Title: "Outbound POST",
		Why: "POST to https://telemetry.example.com/collect",
	}
	malicious := approved
	malicious.Why = "POST to https://discord.com/api/webhooks/1234"

	if fingerprint(approved, "/pkg") == fingerprint(malicious, "/pkg") {
		t.Fatal("two findings differing only in evidence collided; the malicious one would be swallowed by its approved sibling")
	}
}

func TestFingerprintIsIndependentOfTheInstallPath(t *testing.T) {
	at := func(root string) string {
		return fingerprint(Finding{
			RuleID: "R1", File: root + "/scripts/a.py", Title: "T",
			Why: "call in " + root + "/scripts/a.py",
		}, root)
	}
	if at("/home/alice/pkg") != at("/var/ci/build/9/pkg") {
		t.Fatal("the same finding fingerprinted differently at two install paths; every CI run would re-block")
	}
}

// The residual case the evidence digest cannot separate: byte-identical
// findings appearing twice. Without occurrence, adding a duplicate of an
// approved finding would be invisible.
func TestWithOccurrencesDisambiguatesIdenticalFindings(t *testing.T) {
	f := Finding{RuleID: "R1", File: "a.py", Title: "T", Why: "W"}
	got := withOccurrences([]Finding{f, f, f}, "/pkg")
	if len(got) != 3 {
		t.Fatalf("want 3 findings, got %d", len(got))
	}
	seen := map[string]bool{}
	for i, g := range got {
		if g.Occurrence != i {
			t.Errorf("finding %d has occurrence %d", i, g.Occurrence)
		}
		fp := fingerprint(g, "/pkg")
		if seen[fp] {
			t.Fatalf("finding %d collided with an earlier identical finding", i)
		}
		seen[fp] = true
	}
}

func TestWithOccurrencesNormalisesTheFilePath(t *testing.T) {
	got := withOccurrences([]Finding{{RuleID: "R1", File: "/pkg/scripts/a.py"}}, "/pkg")
	if got[0].File != "scripts/a.py" {
		t.Errorf("File = %q, want scripts/a.py", got[0].File)
	}
	// A file that normalises away to the empty string must keep its
	// ORIGINAL value, or unrelated findings would collide on "". The input
	// here is the package root itself, which normalises to "" -- an empty
	// input would not distinguish "preserved" from "overwritten".
	got = withOccurrences([]Finding{{RuleID: "R1", File: "/pkg"}}, "/pkg")
	if got[0].File != "/pkg" {
		t.Errorf("File = %q, want the original %q preserved when normalisation empties it", got[0].File, "/pkg")
	}
}

// Occurrence indices are assigned in input order, so the canonical order
// -- detector findings first, invisible findings second -- is part of
// the contract. Swapping it renumbers findings and invalidates every
// approved fingerprint.
//
// The findings here are byte-IDENTICAL apart from position. An earlier
// version of this test compared two findings that differed in rule,
// title and evidence, so it passed even with occurrence assignment
// deleted entirely -- it proved nothing.
func TestWithOccurrencesIsOrderSensitive(t *testing.T) {
	same := Finding{RuleID: "R1", File: "a.py", Title: "T", Why: "W"}
	other := Finding{RuleID: "R2", File: "b.py", Title: "T2", Why: "W2"}

	got := withOccurrences([]Finding{same, other, same}, "/pkg")
	if len(got) != 3 {
		t.Fatalf("want 3 findings, got %d", len(got))
	}
	// Positions 0 and 2 are identical findings, so they must receive
	// distinct indices; position 1 is unrelated and must start at zero.
	if got[0].Occurrence != 0 || got[1].Occurrence != 0 || got[2].Occurrence != 1 {
		t.Fatalf("occurrences = [%d %d %d], want [0 0 1]",
			got[0].Occurrence, got[1].Occurrence, got[2].Occurrence)
	}

	// Moving the duplicate pair apart must not change which of them is
	// first, but removing the leading duplicate must renumber the trailing
	// one -- proving the index depends on position, not on identity alone.
	shifted := withOccurrences([]Finding{other, same}, "/pkg")
	if shifted[1].Occurrence != 0 {
		t.Fatalf("occurrence = %d, want 0: indices must be assigned by position within a key", shifted[1].Occurrence)
	}
	if fingerprint(got[2], "/pkg") == fingerprint(shifted[1], "/pkg") {
		t.Fatal("the same finding at occurrence 1 and occurrence 0 fingerprinted identically")
	}
}
