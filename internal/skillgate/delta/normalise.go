package delta

import (
	"path/filepath"
	"strings"
)

// normalisePath removes the package's absolute install path from a
// detector-supplied string, so a finding fingerprints identically no
// matter where the package was checked out.
//
// Three steps, reproducing the source implementation exactly:
//
//  1. every occurrence of "<pkgAbs><separator>" is removed outright;
//  2. every REMAINING occurrence of "<pkgAbs>" that is not immediately
//     followed by a filename-continuation byte is removed;
//  3. leading slashes are trimmed.
//
// Step 2 is a negative lookahead, and Go's RE2 has none. DO NOT rewrite
// it with regexp. Every regexp form CONSUMES the terminator: matching
// `pkgAbs + "[^A-Za-z0-9_.-]"` turns `open("/pkg")` into `open("` rather
// than `open("")`, and the alternation `pkgAbs + "($|[^A-Za-z0-9_.-])"`
// does the same on its non-empty branch. The evidence text is a
// fingerprint input, so mangling it there silently changes fingerprints
// and re-blocks approved findings.
//
// The naive version of step 1 -- stripping pkgAbs wherever it appeared,
// without requiring the separator -- was a real bug: package /s/rr/foo
// ate the prefix inside /s/rr/foobar/module.py, mangling unrelated
// evidence and collapsing distinct findings onto one fingerprint. The
// separator requirement plus the step-2 boundary check is what prevents
// that.
//
// pkgAbs is expected already absolute and cleaned.
func normalisePath(value, pkgAbs string) string {
	if value == "" {
		return ""
	}
	v := value
	if pkgAbs != "" {
		v = strings.ReplaceAll(v, pkgAbs+string(filepath.Separator), "")
		v = stripUnanchored(v, pkgAbs)
	}
	return strings.TrimLeft(v, "/")
}

// stripUnanchored removes every occurrence of needle in s that is not
// immediately followed by a filename-continuation character. It scans
// left to right, non-overlapping, advancing past each match -- the same
// traversal a JavaScript /needle(?!...)/g replacement performs.
//
// Byte-wise scanning is safe for multi-byte UTF-8: every continuation
// byte is >= 0x80 and so never matches a continuation character, and the
// needle cannot match at a non-boundary offset because its own first
// byte is ASCII.
func stripUnanchored(s, needle string) string {
	if needle == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], needle) && !continuesFilename(s, i+len(needle)) {
			i += len(needle)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// continuesFilename reports whether s[i] could continue a filename.
//
// End of string continues nothing, so a package root at the very end of
// the value IS stripped -- matching the JavaScript lookahead, which
// succeeds at end of input. Anything else -- a quote, paren, colon,
// comma, semicolon, space -- terminates the path and allows the strip,
// which is what keeps install paths out of quoted and punctuated
// detector evidence.
func continuesFilename(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	switch c := s[i]; {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '_', c == '.', c == '-':
		return true
	default:
		return false
	}
}

// collapseSpace reduces every run of whitespace to a single space and
// trims the ends, so detector evidence that differs only in wrapping
// fingerprints identically.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
