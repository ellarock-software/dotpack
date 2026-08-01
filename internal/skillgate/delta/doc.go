// Package delta implements dotpack's default skill security gate
// (ADR-0016): it approves a package at a reviewed state and blocks only
// on findings that are NEW relative to that approval.
//
// # Why delta rather than absolute
//
// Gating on absolute findings requires a precise detector. Measured on a
// 216-package corpus, the incumbent absolute gate ran about 5% precision
// — 271 findings hand-triaged against the cited file and line, 14 real.
// The response to that much noise is a whole-package bypass, and a
// permanent bypass is strictly worse than a noisy gate: it turns the
// packages most worth watching into the ones never scanned.
//
// Delta inverts the requirement. A constant noise floor is baselined
// once and never fires again, so precision stops mattering and recall
// starts. Any change to a trusted external skill becomes an explicit
// review event.
//
// # Provenance
//
// Ported from skillgate.mjs (499 lines) in the ellarock-config
// repository at commit cccd07fa076a, together with its policy document
// and its 37-case test suite. Three adversarial review rounds against
// that implementation each found a working bypass in the previous
// round's fixes; the fixes are reproduced here and pinned by tests:
//
//   - a symlinked SKILL.md, hashed by nothing and scanned by nothing,
//     allowing approval with zero bytes read;
//   - a package .gitignore listing SKILL.md, letting a full rewrite
//     report "content identical to approved baseline";
//   - an unconditional exclusion allowlist, giving every package a free
//     logs/ hiding place;
//   - a fingerprint keyed on rule/file/title only, silently swallowing a
//     second finding of the same rule in an approved file;
//   - a path-normalisation strip that ate substrings of sibling paths,
//     and then one that missed quote, paren, colon and comma
//     terminators.
//
// # Deliberate divergences from the source
//
//  1. Directory-entry ordering. JavaScript sorts readdir entries by
//     UTF-16 code unit; filepath.WalkDir sorts by UTF-8 byte. The two
//     disagree above U+FFFF, where surrogates sort before U+E000-U+FFFF
//     in UTF-16 and after it in UTF-8. Go's byte order is adopted and
//     pinned by a golden-digest test. This changes the content hash only
//     for packages containing astral-plane filenames; baselines are
//     regenerated for this port regardless, because they move
//     repository, gain provenance fields, and are re-taken against a
//     pinned detector version.
//
//  2. An unreadable directory. The source swallowed the error and
//     returned partial results, so part of a tree could be approved
//     having never been inspected. Here it is BLOCKED, consistent with
//     the existing rule for an unreadable symlink.
//
// # Failure posture
//
// Fail-closed throughout. No SKILL.md, an unsafe symlink, zero files
// hashed, a detector that is missing, times out, crashes or emits
// unparseable output, and the absence of an approved baseline all
// block. An unscannable package is not an approved package.
package delta
