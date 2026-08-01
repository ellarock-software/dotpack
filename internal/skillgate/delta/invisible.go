package delta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// invisibleNames is the invisible and bidi-control codepoint table.
// None of these is ever legitimate in a skill package.
//
// It is a SLICE, not a map. Emission order determines the occurrence
// index a finding receives, and Go randomises map iteration -- a map
// here would produce a different set of fingerprints on every run, so a
// package would intermittently fail to match its own baseline.
var invisibleNames = []struct {
	CP   rune
	Name string
}{
	{0x200b, "ZERO WIDTH SPACE"},
	{0x200c, "ZERO WIDTH NON-JOINER"},
	{0x200d, "ZERO WIDTH JOINER"},
	{0x2060, "WORD JOINER"},
	{0xfeff, "ZERO WIDTH NO-BREAK SPACE (BOM)"},
	{0x00ad, "SOFT HYPHEN"},
	{0x180e, "MONGOLIAN VOWEL SEPARATOR"},
	{0x061c, "ARABIC LETTER MARK"},
	{0x200e, "LEFT-TO-RIGHT MARK"},
	{0x200f, "RIGHT-TO-LEFT MARK"},
	{0x202a, "LEFT-TO-RIGHT EMBEDDING"},
	{0x202b, "RIGHT-TO-LEFT EMBEDDING"},
	{0x202c, "POP DIRECTIONAL FORMATTING"},
	{0x202d, "LEFT-TO-RIGHT OVERRIDE"},
	{0x202e, "RIGHT-TO-LEFT OVERRIDE"},
	{0x2066, "LEFT-TO-RIGHT ISOLATE"},
	{0x2067, "RIGHT-TO-LEFT ISOLATE"},
	{0x2068, "FIRST STRONG ISOLATE"},
	{0x2069, "POP DIRECTIONAL ISOLATE"},
}

var invisibleByCP = func() map[rune]string {
	m := make(map[rune]string, len(invisibleNames))
	for _, e := range invisibleNames {
		m[e.CP] = e.Name
	}
	return m
}()

// invisibleSkipDirs are never searched. Distinct from the hash
// exclusions on purpose: those are earned per package, these are
// third-party trees where a hit would be noise. Note that "logs" is NOT
// here, which is how a runtime directory stays excluded from the hash
// while still being inspected for hidden codepoints.
var invisibleSkipDirs = newSet([]string{".git", "node_modules", ".venv", "__pycache__"})

// maxReportedLines caps the line list in one finding. The count field
// still carries the true total.
const maxReportedLines = 20

// codepointTally accumulates line numbers per codepoint in first-seen
// order.
type codepointTally struct {
	order []rune
	lines map[rune][]int
}

func newCodepointTally() *codepointTally {
	return &codepointTally{lines: map[rune][]int{}}
}

func (t *codepointTally) add(cp rune, line int) {
	if _, seen := t.lines[cp]; !seen {
		t.order = append(t.order, cp)
	}
	t.lines[cp] = append(t.lines[cp], line)
}

// scanInvisible searches a package for hidden codepoints.
//
// This check is deterministic and runs in process, and it must stay that
// way: the class CANNOT be delegated to a semantic analyser, because an
// invisible codepoint is invisible in the analyser's input too. Measured
// against the source implementation, the detector with its LLM engine
// enabled still missed 21 zero-width spaces in a real skill.
//
// Homoglyphs are reported for SKILL.md only. That file is the
// instruction surface an agent actually reads, so a Latin-looking
// Cyrillic letter there can disguise an instruction or defeat keyword
// matching; elsewhere in a package, non-Latin script is ordinary content.
func scanInvisible(pkgAbs string, files []string, p Policy) []Finding {
	suffixes := newSet(p.Invisible.ScanSuffixes)
	severity := p.Invisible.Severity
	if severity == "" {
		severity = "HIGH"
	}

	var findings []Finding
	for _, abs := range files {
		rel, err := filepath.Rel(pkgAbs, abs)
		if err != nil {
			continue
		}
		if hasSkippedSegment(rel) {
			continue
		}
		if !suffixes.has(strings.ToLower(filepath.Ext(abs))) {
			continue
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			continue
		}

		hidden := newCodepointTally()
		homoglyph := newCodepointTally()
		for i, line := range strings.Split(string(raw), "\n") {
			for _, ch := range line {
				switch {
				case invisibleByCP[ch] != "":
					hidden.add(ch, i+1)
				case ch > 127 && isConfusableScript(ch):
					homoglyph.add(ch, i+1)
				}
			}
		}

		for _, cp := range hidden.order {
			ls := hidden.lines[cp]
			findings = append(findings, Finding{
				RuleID:   fmt.Sprintf("INVISIBLE_CHAR_U+%s", codepointHex(cp)),
				Severity: severity,
				Analyzer: "skillgate.invisible",
				File:     rel,
				Lines:    uniqueCapped(ls),
				Count:    len(ls),
				Title:    fmt.Sprintf("%s (U+%s) embedded in text", invisibleByCP[cp], codepointHex(cp)),
				Why:      "Invisible to a human reviewer and to an LLM analyser. Used to hide instructions or to split keywords so literal pattern matching fails.",
			})
		}

		if filepath.Base(abs) == "SKILL.md" {
			for _, cp := range homoglyph.order {
				ls := homoglyph.lines[cp]
				findings = append(findings, Finding{
					RuleID:   "HOMOGLYPH_MIXED_SCRIPT",
					Severity: severity,
					Analyzer: "skillgate.invisible",
					File:     rel,
					Lines:    uniqueCapped(ls),
					Count:    len(ls),
					Title:    fmt.Sprintf("Non-Latin letter U+%s in SKILL.md", codepointHex(cp)),
					Why:      "Cyrillic/Greek letters that render identically to Latin can disguise instructions or defeat keyword matching.",
				})
			}
		}
	}
	return findings
}

// isConfusableScript reports whether r belongs to a script whose letters
// commonly render identically to Latin.
func isConfusableScript(r rune) bool {
	return unicode.Is(unicode.Cyrillic, r) || unicode.Is(unicode.Greek, r)
}

func hasSkippedSegment(relPath string) bool {
	for _, seg := range strings.Split(relPath, string(filepath.Separator)) {
		if invisibleSkipDirs.has(seg) {
			return true
		}
	}
	return false
}

func codepointHex(cp rune) string {
	return fmt.Sprintf("%04X", cp)
}

func uniqueCapped(lines []int) []int {
	seen := make(map[int]struct{}, len(lines))
	out := make([]int, 0, len(lines))
	for _, l := range lines {
		if _, ok := seen[l]; ok {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
		if len(out) == maxReportedLines {
			break
		}
	}
	return out
}
