// Format-aware merged-key apply / un-merge per ADR-0012 §5–§7 + §9.
//
// Why this lives in orchestrator (not adapter): the read-modify-write
// algorithm is FORMAT-specific (JSON vs TOML), not host-specific.
// Adapters emit (file, path, value) tuples in their native path syntax;
// the orchestrator infers format from the file extension and dispatches
// to the right walker. This is the advisor's pushback on ADR-0012 §9's
// "Uninstall...requires the host adapter" future-note — empirically the
// walker is sufficient, so Reader.Uninstall does NOT need an adapter
// parameter for the un-merge step.
//
// The card #3 future-note remains accurate ("when hook + mcp-server
// kinds land, Uninstall WILL gain an un-merge-keys step") — it just
// doesn't escalate Uninstall to Installer, because the un-merge is
// format-specific and the manifest record already carries the
// self-sufficient (File, Path) tuples.
package orchestrator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/manifest"
)

// mergedFormat identifies the read-modify-write encoding for a target
// config file. Inferred from the file extension at apply / un-merge
// time; adapters do not declare format on each MergedKeyWrite because
// the file extension is already an unambiguous source of truth (and
// would diverge dangerously from a per-MergedKeyWrite declaration if
// they disagreed).
type mergedFormat int

const (
	mergedFormatJSON mergedFormat = iota
	mergedFormatTOML
)

// formatFromFile returns the merged-key encoding for a target config
// file. .json → JSON, .toml → TOML. Unknown extensions return a
// structured error rather than defaulting — silently treating an
// unknown extension as JSON would garble TOML (and vice versa).
func formatFromFile(path string) (mergedFormat, error) {
	switch filepath.Ext(path) {
	case ".json":
		return mergedFormatJSON, nil
	case ".toml":
		return mergedFormatTOML, nil
	default:
		return 0, fmt.Errorf("merged-key apply: unsupported file extension for %s (only .json and .toml are wired today)", path)
	}
}

// applyMergedKey performs the read-modify-write for one (file, path,
// value) tuple. Empty / absent file is treated as an empty root {}, so
// fresh-user .mcp.json installs work without a pre-existing file. The
// rewrite is atomic via writeAtomic (write tmp, fsync, rename).
//
// Dispatches on Op:
//   - MergedKeySet (default): path-leaf overwrite — mcp-server's shape.
//   - MergedKeyAppend: path target is an array; value is appended —
//     hook's $.hooks.<event> shape per ADR-0012 §9.
func applyMergedKey(mk adapter.MergedKeyWrite) error {
	format, err := formatFromFile(mk.File)
	if err != nil {
		return err
	}
	switch format {
	case mergedFormatJSON:
		return applyJSONMergedKey(mk)
	case mergedFormatTOML:
		return applyTOMLMergedKey(mk)
	default:
		return fmt.Errorf("merged-key apply: unknown format %v", format)
	}
}

// applyJSONMergedKey reads the target JSON file (or treats it as {} when
// absent), applies the value at the parsed path per Op, and atomic-
// writes the re-marshalled bytes. Intermediate maps are created as
// needed; a non-map intermediate (e.g., user manually set $.mcpServers
// to a string) returns a structured error rather than overwriting.
func applyJSONMergedKey(mk adapter.MergedKeyWrite) error {
	root, err := readJSONOrEmpty(mk.File)
	if err != nil {
		return err
	}
	path, err := parseJSONPath(mk.Path)
	if err != nil {
		return fmt.Errorf("merged-key apply: parse path %q: %w", mk.Path, err)
	}
	switch mk.Op {
	case adapter.MergedKeySet:
		if err := setJSONPath(root, path, mk.Value); err != nil {
			return fmt.Errorf("merged-key apply: set %q in %s: %w", mk.Path, mk.File, err)
		}
	case adapter.MergedKeyAppend:
		if err := appendJSONPath(root, path, mk.Value); err != nil {
			return fmt.Errorf("merged-key apply: append %q in %s: %w", mk.Path, mk.File, err)
		}
	default:
		return fmt.Errorf("merged-key apply: unknown op %q for %s", mk.Op, mk.File)
	}
	return writeJSON(mk.File, root)
}

// unmergeJSONKey is uninstall's inverse: read the file, delete the
// leaf at path (Op=Set) OR remove the array element whose content-hash
// matches the manifest's stored Selector (Op=Append), atomic-write
// the result. Absent file is a no-op (user manually removed it;
// uninstall doesn't second-guess). Absent leaf / no-matching-element
// is a no-op (already cleaned up; uninstall is idempotent).
//
// File-retention policy: when the root map is empty after deletion
// (no sibling entries), the file is LEFT in place with `{}` content.
// Alternative (delete the file when empty) was considered and rejected
// because dotpack doesn't own .mcp.json — the user may have intentionally
// created it and expects it to exist even when transiently empty. The
// filedrop TargetDir cleanup is asymmetric here because filedrop OWNS
// the per-name subdir; config-fragment installs do NOT own the target
// file.
//
// Hook-specific drift tolerance: when Op=Append AND no array element
// hashes to the stored Selector, uninstall is a no-op (user edited the
// installed binding so its content changed — leave their edit alone).
// Same principle as the deleteJSONPath non-map-intermediate tolerance:
// "remove this install's claim" rather than "force a structural reset".
func unmergeJSONKey(mk MergedKeySelector) error {
	raw, err := os.ReadFile(mk.File)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("merged-key un-merge: read %s: %w", mk.File, err)
	}
	if len(raw) == 0 {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("merged-key un-merge: parse %s: %w", mk.File, err)
	}
	if root == nil {
		return nil
	}
	path, err := parseJSONPath(mk.Path)
	if err != nil {
		return fmt.Errorf("merged-key un-merge: parse path %q: %w", mk.Path, err)
	}
	var changed bool
	switch mk.Op {
	case adapter.MergedKeySet:
		c, err := deleteJSONPath(root, path)
		if err != nil {
			return fmt.Errorf("merged-key un-merge: delete %q in %s: %w", mk.Path, mk.File, err)
		}
		changed = c
	case adapter.MergedKeyAppend:
		if mk.Selector == "" {
			return fmt.Errorf("merged-key un-merge: %s op=append requires a Selector in the manifest (this is a manifest-shape bug; install should never have persisted an append-MergedKey without one)", mk.Path)
		}
		c, err := removeJSONArrayElementBySelector(root, path, mk.Selector)
		if err != nil {
			return fmt.Errorf("merged-key un-merge: remove array element at %q in %s: %w", mk.Path, mk.File, err)
		}
		changed = c
	default:
		return fmt.Errorf("merged-key un-merge: unknown op %q for %s", mk.Op, mk.File)
	}
	// Skip the write when nothing was removed — preserves user-authored
	// bytes (mode, whitespace, key order, string-quote style) on
	// idempotent uninstall. Hostile-review #5 from THIS slice. Symmetric
	// with the no-op-no-write posture in unmergeTOMLKey.
	if !changed {
		return nil
	}
	return writeJSON(mk.File, root)
}

// MergedKeySelector is the read-only shape Reader.Uninstall uses to
// invoke unmergeKey. It mirrors adapter.MergedKeyWrite minus Value
// (un-merge does not need the original value — Selector is the
// identity for Op=Append; Path alone identifies the leaf for Op=Set).
// The split keeps the Reader side from depending on the adapter
// package's write-time type.
type MergedKeySelector struct {
	File     string
	Path     string
	Op       adapter.MergedKeyOp
	Selector string
}

// unmergeKey dispatches by file format, mirroring applyMergedKey.
func unmergeKey(mk MergedKeySelector) error {
	format, err := formatFromFile(mk.File)
	if err != nil {
		return err
	}
	switch format {
	case mergedFormatJSON:
		return unmergeJSONKey(mk)
	case mergedFormatTOML:
		return unmergeTOMLKey(mk)
	default:
		return fmt.Errorf("merged-key un-merge: unknown format %v", format)
	}
}

// readJSONOrEmpty reads a JSON file into a map. Absent file → empty
// map. Empty / whitespace-only file → empty map. Non-object root →
// structured error (the merged-key walkers all assume a map root).
func readJSONOrEmpty(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse %s as JSON object: %w", path, err)
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

// writeJSON atomic-writes root to path with stable two-space indent +
// trailing newline. Reuses writeAtomic for the tmp-write/rename pair
// (same crash-safety guarantee filedrop installs get).
//
// Mode preservation (hostile-review #2): when the target file already
// exists, we preserve its mode rather than forcing 0o644. .mcp.json
// can carry inlined credentials (per schema/mcp-server.yaml's
// ecosystem_notes — abcdan-style `--figma-api-key=XXXX` in args; the
// arc-kit `headers={Authorization=Bearer ...}` shape; etc.) and a user
// who set 0o600 expects that to survive the rewrite. Fresh files
// default to 0o644 — matching the writeAtomic fallback when fw.Mode==0.
//
// SetEscapeHTML(false) on the encoder (hostile-review #6) keeps `&`,
// `<`, `>` literal in the written bytes, so URLs and shell-expansion
// characters in `args` don't render as & / < / > —
// visually identical when re-parsed, but trust-eroding when a user
// diffs the file before/after dotpack touches it.
func writeJSON(path string, root map[string]any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	mode := preserveMode(path, 0o644)
	return writeAtomic(adapter.FileWrite{Path: path, Content: buf.Bytes(), Mode: mode})
}

// preserveMode returns the existing file's mode if present, otherwise
// the supplied default. Used by writeJSON to avoid silently relaxing
// 0o600 on credential-bearing host config files.
func preserveMode(path string, dflt os.FileMode) os.FileMode {
	st, err := os.Stat(path)
	if err != nil {
		return dflt
	}
	return st.Mode().Perm()
}

// applyTOMLMergedKey is the TOML mirror of applyJSONMergedKey. The map-
// walk primitives (setJSONPath, getJSONPath, deleteJSONPath,
// appendJSONPath, removeJSONArrayElementBySelector) are
// FORMAT-AGNOSTIC — they walk map[string]any by []string segments and
// don't reference JSON semantics, so TOML reuses them after
// parseTOMLPath does the format-specific path parsing. The "JSON" in
// their name is historical; renaming would churn ~14 callsites for no
// functional gain.
//
// normalizeForTOML applies HERE — to the install's value only, NOT to
// the full root. The root contains user-authored content (e.g.,
// `version = 1.0` at top-level); normalizing that would coerce the
// user's explicit float to int and silently mutate bytes dotpack does
// not own. Hostile-review #1 from slice v18 (codex mcp-server). Apply
// only to mk.Value because that's the JSON-sourced fragment whose
// integral-float64 arrival shape needs coercion before TOML emit.
//
// Op=Set + Op=Append both fully wired today:
//   - Op=Set: codex mcp-server (Set into mcp_servers.<name>).
//   - Op=Append: codex hook (Append into hooks.<Event> array-of-tables).
//
// Selector hash stability across normalize: buildRecord computes
// Selector from the UN-normalized mk.Value (one site, format-agnostic).
// For all hook universal-core types (string, int, map[string]string,
// []any-of-map[string]any) normalize is an identity transform, so the
// install-time hash matches the uninstall-time hash (re-derived from
// the TOML-roundtripped value). The probe in probe_toml_aot_test.go
// pins this for the four shapes that exist today: bare matcher+command,
// env-bearing, timeout-bearing, and append-into-existing.
//
// Two future scenarios would break this invariant — both produce silent
// un-merge orphans where the manifest record clears but the on-disk
// element survives:
//
//  1. Non-integral float64 in mk.Value (e.g., `timeout: 1.5` if the
//     schema ever admits sub-second timeouts). The install hash is
//     float64-shaped; the uninstall hash post-normalize is int64-
//     shaped only when integral, and float64-shaped otherwise — but the
//     TOML roundtrip preserves the float64 either way, so the actual
//     divergence is via the integral-coercion path.
//  2. Explicit `nil` map values in mk.Value (e.g., `"foo": null` in
//     a hook extension surface). normalizeForTOML DROPS nil map values
//     silently; TOML emits without `foo`; uninstall re-reads without
//     `foo`; hash excludes `foo` while install-time hash included it.
//
// At that point the fix is either (a) compute Selector from
// normalizeForTOML(mk.Value) at buildRecord, or (b) refuse the
// offending shape (float64, nil map value) at the validator before it
// reaches the merge boundary. Documented here so a future reader
// hitting the orphan-uninstall ghost has a pointer.
func applyTOMLMergedKey(mk adapter.MergedKeyWrite) error {
	root, err := readTOMLOrEmpty(mk.File)
	if err != nil {
		return err
	}
	path, err := parseTOMLPath(mk.Path)
	if err != nil {
		return fmt.Errorf("merged-key apply: parse path %q: %w", mk.Path, err)
	}
	switch mk.Op {
	case adapter.MergedKeySet:
		normalizedValue, err := normalizeForTOML(mk.Value)
		if err != nil {
			return fmt.Errorf("merged-key apply: normalize value for %q in %s: %w", mk.Path, mk.File, err)
		}
		if err := setJSONPath(root, path, normalizedValue); err != nil {
			return fmt.Errorf("merged-key apply: set %q in %s: %w", mk.Path, mk.File, err)
		}
	case adapter.MergedKeyAppend:
		normalizedValue, err := normalizeForTOML(mk.Value)
		if err != nil {
			return fmt.Errorf("merged-key apply: normalize value for %q in %s: %w", mk.Path, mk.File, err)
		}
		if err := appendJSONPath(root, path, normalizedValue); err != nil {
			return fmt.Errorf("merged-key apply: append %q in %s: %w", mk.Path, mk.File, err)
		}
	default:
		return fmt.Errorf("merged-key apply: unknown op %q for %s", mk.Op, mk.File)
	}
	return writeTOML(mk.File, root)
}

// unmergeTOMLKey is the TOML mirror of unmergeJSONKey. Absent file is a
// no-op (uninstall is idempotent). Same drift-tolerance principles:
// non-map intermediates on the delete path are no-ops (user manually
// nulled out the parent); missing leaves are no-ops; Op=Append's
// no-matching-hash is a no-op (user edited the binding).
//
// Op=Append re-uses removeJSONArrayElementBySelector — format-agnostic
// because the walker only inspects map[string]any / []any. selectorFor
// hashes the TOML-roundtripped value identically to the install-time
// hash for hook universal-core types (see applyTOMLMergedKey's
// Selector-stability note).
func unmergeTOMLKey(mk MergedKeySelector) error {
	raw, err := os.ReadFile(mk.File)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("merged-key un-merge: read %s: %w", mk.File, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("merged-key un-merge: parse %s as TOML: %w", mk.File, err)
	}
	if root == nil {
		return nil
	}
	path, err := parseTOMLPath(mk.Path)
	if err != nil {
		return fmt.Errorf("merged-key un-merge: parse path %q: %w", mk.Path, err)
	}
	var changed bool
	switch mk.Op {
	case adapter.MergedKeySet:
		c, err := deleteJSONPath(root, path)
		if err != nil {
			return fmt.Errorf("merged-key un-merge: delete %q in %s: %w", mk.Path, mk.File, err)
		}
		changed = c
	case adapter.MergedKeyAppend:
		if mk.Selector == "" {
			return fmt.Errorf("merged-key un-merge: %s op=append requires a Selector in the manifest (this is a manifest-shape bug; install should never have persisted an append-MergedKey without one)", mk.Path)
		}
		c, err := removeJSONArrayElementBySelector(root, path, mk.Selector)
		if err != nil {
			return fmt.Errorf("merged-key un-merge: remove array element at %q in %s: %w", mk.Path, mk.File, err)
		}
		changed = c
	default:
		return fmt.Errorf("merged-key un-merge: unknown op %q for %s", mk.Op, mk.File)
	}
	// Skip the write when nothing was removed — preserves user-authored
	// bytes (mode, whitespace, key order, integer-vs-float syntax) on
	// idempotent uninstall. Hostile-review #5 from THIS slice. Especially
	// load-bearing on TOML because go-toml/v2's emit normalizes string
	// quote style (`"x"` → `'x'`) and re-sorts keys — touching the file
	// would be visible noise in the user's diff for a no-op operation.
	if !changed {
		return nil
	}
	return writeTOML(mk.File, root)
}

// readTOMLOrEmpty is the TOML mirror of readJSONOrEmpty. Absent file →
// empty map. Empty / whitespace-only → empty map. Non-table root →
// structured error.
//
// Empirically (probe in /tmp/dotpack-toml-probe before this slice
// landed), pelletier/go-toml/v2 produces map[string]any with:
//   - int64 for integers (NOT float64 like JSON's default)
//   - float64 for non-integral floats
//   - bool, string natively
//   - []any for arrays
//   - map[string]any for tables / inline tables / array-of-tables
//
// The format-agnostic map walkers (setJSONPath etc.) accept all of
// these because they only inspect map[string]any and []any, never
// numeric types.
func readTOMLOrEmpty(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse %s as TOML: %w", path, err)
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

// writeTOML serialises root → bytes via pelletier/go-toml/v2 and atomic-
// writes to path. Mirrors writeJSON's mode preservation policy for
// credential-bearing files.
//
// IMPORTANT: writeTOML does NOT walk root applying normalizeForTOML.
// Earlier drafts did, motivated by the JSON-source-into-TOML-target
// float64-to-`30.0` problem. Hostile-review #1 (this slice): walking
// the whole root coerces user-authored TOML floats too — `version =
// 1.0` (which toml.Unmarshal reports as float64(1.0)) gets demoted to
// `version = 1` on the next dotpack write. Direct violation of "we
// don't own this file." The coercion now lives at the merge-boundary
// in applyTOMLMergedKey — applied to mk.Value only — so JSON-sourced
// install values are coerced while user-authored content passes
// through untouched.
//
// Comment preservation: NOT pursued. go-toml/v2's Unmarshal(_, &map[
// string]any) strips comments, so a hand-authored `# blah` line in
// ~/.codex/config.toml is lost on the first dotpack-touched write.
// Mirrors writeJSON's "no comments in JSON" policy — dotpack-managed
// host config files are read-modify-write through the structured
// representation; users wanting persistent comments should keep them
// in a separate `~/.codex/config.toml.local` (codex spec allows
// per-project overlays; the schema notes alternate_files).
func writeTOML(path string, root map[string]any) error {
	out, err := toml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal %s as TOML: %w", path, err)
	}
	mode := preserveMode(path, 0o644)
	return writeAtomic(adapter.FileWrite{Path: path, Content: out, Mode: mode})
}

// normalizeForTOML walks a JSON-shaped value (map[string]any / []any /
// scalars from json.Unmarshal) and coerces it into a TOML-marshalable
// shape:
//
//   - integral float64 (e.g., 30 from JSON-decode of `30`) → int64. Non-
//     integral float64 (30.5) stays float64. Non-finite (Inf/NaN) errors
//     — TOML has no representation.
//   - nil at a MAP key → drop the key. go-toml/v2 silently drops nil
//     scalars too, but explicit drop is symmetric with TOML semantics
//     and surfaces the loss in the diff (the key just isn't there in
//     the output) rather than emitting a confusing `key = `.
//   - nil at a SLICE index → error. go-toml/v2 errors with "encoding a
//     nil interface is not supported"; we surface a structured wrapper
//     with the index for debuggability.
//   - maps recurse; slices recurse; everything else (int64, bool,
//     string, already-coerced types) passes through.
//
// Why a custom walker and not just toml.Marshal's behavior: (a) the
// integer-vs-float distinction is invisible to JSON (`30` and `30.0`
// both deserialize to float64) but VISIBLE in TOML's on-disk format,
// so a user diffing config.toml sees integer fields suddenly become
// floats. (b) silent nil-drop on map values is fine; silent error on
// slice nils isn't — we want the install to fail with a clear pointer
// to the offending source rather than go-toml/v2's terse default.
//
// Apply ONLY to JSON-sourced values at the merge boundary —
// applyTOMLMergedKey runs this on mk.Value before setJSONPath. Do NOT
// apply to the full root: toml.Unmarshal preserves user-authored
// integer-vs-float distinctions in the returned map (int64 for ints,
// float64 for floats), and walking the root would coerce the user's
// `version = 1.0` to `1` (hostile-review #1 this slice — silent
// corruption of bytes dotpack does not own).
func normalizeForTOML(v any) (any, error) {
	switch x := v.(type) {
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil, fmt.Errorf("non-finite float %v has no TOML representation", x)
		}
		if x == math.Trunc(x) && x >= math.MinInt64 && x <= math.MaxInt64 {
			return int64(x), nil
		}
		return x, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			if vv == nil {
				continue
			}
			nv, err := normalizeForTOML(vv)
			if err != nil {
				return nil, fmt.Errorf("at .%s: %w", k, err)
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(x))
		for i, item := range x {
			if item == nil {
				return nil, fmt.Errorf("at [%d]: nil array element has no TOML representation", i)
			}
			nv, err := normalizeForTOML(item)
			if err != nil {
				return nil, fmt.Errorf("at [%d]: %w", i, err)
			}
			out = append(out, nv)
		}
		return out, nil
	default:
		return v, nil
	}
}

// parseTOMLPath splits a dotted TOML path ("mcp_servers.foo") into
// ["mcp_servers", "foo"]. NO leading `$` (that's the JSON-syntax
// marker). Empty path / empty segments rejected.
//
// Defensive: an adapter that mistakenly emits a JSON-syntax path
// ("$.mcp_servers.foo") into a TOML-format KindConfig surfaces here
// rather than producing a top-level `$` table entry. The cross-format
// safety net complements configfrag.New's policy validator (which
// catches WIRING bugs at construction) — this catches EMIT bugs at
// apply time.
func parseTOMLPath(p string) ([]string, error) {
	if p == "" {
		return nil, fmt.Errorf("TOML path is empty (root-only paths cannot be merged into)")
	}
	if strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("TOML path must NOT have $ prefix; got %q (JSON-syntax path passed to TOML walker — adapter emit bug)", p)
	}
	parts := strings.Split(p, ".")
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("TOML path %q has an empty segment at position %d", p, i)
		}
	}
	return parts, nil
}

// parseJSONPath splits a "$.a.b.c" expression into ["a", "b", "c"].
// Dot-segments only. Empty segments, missing $-root, and bare $
// (no trailing path) are rejected — the adapter-side path string is
// the canonical entry point and we want errors to fire there, not
// later at apply time.
//
// NOT a general JSONPath implementation: dotpack's emitted paths are a
// constrained subset (no wildcards, no filters, no descendants).
//
// The hook slice did NOT extend this to array-index syntax
// (`$.hooks.PreToolUse[3]`) as the v15 handoff predicted. Per advisor:
// numeric indices are unstable across sibling installs and user
// reorders, so the hook design uses content-hash identity instead.
// Array semantics moved to Op-based dispatch on adapter.MergedKeyWrite
// (MergedKeySet vs MergedKeyAppend) — the path string stays a clean
// dot-path; the operation chooses set-leaf or append-to-array, and
// the manifest's Selector field carries the identity for un-merge.
//
// Segment-content invariant (hostile-review #8): path segments MUST NOT
// contain a literal `.` — the split happens on `.` so a segment with a
// dot would be silently sliced into two segments. Callers (the
// adapter's emit function) are responsible for ensuring identifiers
// are dot-free. For mcp-server, the validator's mcpServerNameRE
// excludes `.` from server names, so emitMCPServer's
// "$.mcpServers." + Name composition is safe. For future kinds whose
// names CAN contain dots (e.g., reverse-domain identifiers), this
// function gains a bracketed-segment syntax ($.foo["bar.baz"]) — until
// then, no caller has produced such a path.
func parseJSONPath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("path must start with $ (root); got %q", p)
	}
	rest := strings.TrimPrefix(p, "$")
	if rest == "" {
		return nil, fmt.Errorf("path %q has no segments after $ (root-only paths cannot be merged into)", p)
	}
	if !strings.HasPrefix(rest, ".") {
		return nil, fmt.Errorf("path %q: expected . after $; got %q", p, rest)
	}
	rest = strings.TrimPrefix(rest, ".")
	if rest == "" {
		return nil, fmt.Errorf("path %q ends with $.  (trailing dot, no segment)", p)
	}
	parts := strings.Split(rest, ".")
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("path %q has an empty segment at position %d", p, i)
		}
	}
	return parts, nil
}

// setJSONPath sets root[path[0]][path[1]]...[path[-1]] = value. Walks
// the path creating any missing intermediate maps. A non-map intermediate
// is a structured error (e.g., the user manually wrote
// "mcpServers": "foo" — a string, not an object — at the root; trying to
// set "$.mcpServers.github" then fails honestly rather than overwriting).
//
// FORMAT-AGNOSTIC: walks map[string]any by []string segments without
// referencing JSON syntax. Used by both applyJSONMergedKey and
// applyTOMLMergedKey. Error messages render the path with dot-join
// rather than the Go []string default ([%v]) so the user sees their
// adapter-emitted path shape regardless of format. The "JSON" in the
// function name is historical (predates the TOML walker pair).
func setJSONPath(root map[string]any, path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("setJSONPath: empty path")
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		next, ok := cur[seg]
		if !ok {
			sub := map[string]any{}
			cur[seg] = sub
			cur = sub
			continue
		}
		subMap, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("path %s: intermediate segment %q is %T, not a map; refusing to overwrite (manually edit the file or use --force)", strings.Join(path, "."), seg, next)
		}
		cur = subMap
	}
	cur[path[len(path)-1]] = value
	return nil
}

// getJSONPath returns the value at path in root, with an exists flag.
// Walks dot-segments only (no array indexing for tracer-bullet shape).
// A non-map intermediate returns (nil, false) — the path doesn't exist
// in the navigable sense — rather than an error, because callers
// (preflightMergedKeyCollisions) ask "is this slot occupied?" and a
// "no, but the parent is weird" answer would lie.
func getJSONPath(root map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	var cur any = root
	for _, seg := range path {
		subMap, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		val, ok := subMap[seg]
		if !ok {
			return nil, false
		}
		cur = val
	}
	return cur, true
}

// appendJSONPath appends value to the array at root[path[0]]...[path[-1]].
// Missing intermediate maps are created (mirrors setJSONPath). A non-
// map intermediate returns a structured error — refusing to overwrite
// the user's manual structure. Absent leaf (the array slot doesn't
// exist yet) is fine: a fresh empty array is created and value is
// the first element. A leaf that exists but is NOT an array is a
// structured error (the user manually wrote $.hooks = "foo" or
// $.hooks.PreToolUse = {}; can't append to that).
func appendJSONPath(root map[string]any, path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("appendJSONPath: empty path")
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		next, ok := cur[seg]
		if !ok {
			sub := map[string]any{}
			cur[seg] = sub
			cur = sub
			continue
		}
		subMap, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("path %s: intermediate segment %q is %T, not a map; refusing to overwrite (manually edit the file or use --force)", strings.Join(path, "."), seg, next)
		}
		cur = subMap
	}
	leafKey := path[len(path)-1]
	existing, present := cur[leafKey]
	if !present {
		cur[leafKey] = []any{value}
		return nil
	}
	arr, ok := existing.([]any)
	if !ok {
		return fmt.Errorf("path %s: leaf segment %q is %T, not an array; refusing to overwrite (manually edit the file or use --force)", strings.Join(path, "."), leafKey, existing)
	}
	cur[leafKey] = append(arr, value)
	return nil
}

// removeJSONArrayElementBySelector navigates to the array at path and
// removes the FIRST element whose sha256 content-hash matches selector.
// Returns (changed, err) where changed reports whether an element was
// actually found and removed. Stable across sibling installs/uninstalls
// and user reorders — the hash identity survives where a numeric index
// would shift.
//
// Drift tolerance: when no element matches (user edited the binding,
// user deleted it, file is missing entirely up to the array), returns
// (false, nil) and the caller should skip the file write. This is the
// "drift on uninstall is intentional" principle extended to the hook
// case — the alternative (refuse to uninstall when content has
// drifted) would block the user from clearing the manifest record
// even when they manifestly wanted the install gone.
//
// Missing intermediate maps / non-map intermediates / non-array leaves
// are ALSO no-ops on the un-merge path: the array can't navigably
// contain our hash if the structure leading to it doesn't exist, so
// "already gone" is the right interpretation. Asymmetric with apply's
// strictness about non-map intermediates, matching deleteJSONPath's
// docstring.
func removeJSONArrayElementBySelector(root map[string]any, path []string, selector string) (bool, error) {
	if len(path) == 0 {
		return false, fmt.Errorf("removeJSONArrayElementBySelector: empty path")
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		next, ok := cur[seg]
		if !ok {
			return false, nil
		}
		subMap, ok := next.(map[string]any)
		if !ok {
			return false, nil
		}
		cur = subMap
	}
	leafKey := path[len(path)-1]
	existing, present := cur[leafKey]
	if !present {
		return false, nil
	}
	arr, ok := existing.([]any)
	if !ok {
		return false, nil
	}
	for i, el := range arr {
		hash, err := selectorFor(el)
		if err != nil {
			return false, fmt.Errorf("hash array element at index %d: %w", i, err)
		}
		if hash == selector {
			cur[leafKey] = append(arr[:i], arr[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// selectorFor computes the canonical content-hash for an array element.
// "sha256:" + hex(json.Marshal(v)). json.Marshal sorts map keys
// lexicographically, so the hash is stable across re-reads of the
// same JSON file (and across emit functions that return
// map[string]any per the cacheKey docstring's convention).
//
// The function is also called by the orchestrator's buildRecord to
// compute Selector at install time; using the same function on both
// sides guarantees identity. If the underlying hash function ever
// changes (e.g., switching to a content-canonical JSON encoder), the
// manifest persistence shape gains a "version" byte so old selectors
// don't silently mis-match.
func selectorFor(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal for selector: %w", err)
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}

// deleteJSONPath removes root[path[0]][path[1]]...[path[-1]]. Returns
// (changed, err) where changed reports whether the leaf was actually
// present and removed. Walks the path; missing intermediates are a
// no-op-no-change (uninstall is idempotent across already-cleaned
// state) — callers can skip the file write when changed=false.
//
// Non-map intermediates (e.g., the user manually set
// {"mcpServers": null} or {"mcpServers": "foo"}) are ALSO treated as
// no-ops on the delete path — the leaf can't navigably exist past a
// non-map intermediate, so it's "already gone" in the sense uninstall
// cares about. This is asymmetric with setJSONPath, which is strict
// about non-map intermediates because install must not overwrite
// structure the user manually established. Uninstall's contract is
// "remove this install's claim"; refusing because the user nulled out
// the parent would surprise users who expect uninstall to be tolerant.
// Matches the principle documented on Uninstall: "Drift on uninstall is
// intentional" (hostile-review #4 from slice v15).
//
// Empty parent maps are NOT recursively removed — see unmergeJSONKey's
// file-retention policy docstring for the rationale.
//
// changed=true → caller should atomic-write the mutated root.
// changed=false → caller should skip the write; the file is already in
// the desired state. This avoids the hostile-review #5 hygiene gap
// from THIS slice where idempotent uninstall touched bytes unnecessarily
// (key sorting, string quote style flips, etc. — visible noise in the
// user's diff for a no-op operation).
func deleteJSONPath(root map[string]any, path []string) (bool, error) {
	if len(path) == 0 {
		return false, fmt.Errorf("deleteJSONPath: empty path")
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		next, ok := cur[seg]
		if !ok {
			return false, nil
		}
		subMap, ok := next.(map[string]any)
		if !ok {
			return false, nil
		}
		cur = subMap
	}
	leafKey := path[len(path)-1]
	if _, present := cur[leafKey]; !present {
		return false, nil
	}
	delete(cur, leafKey)
	return true, nil
}

// unmergeExistingAppendsForID is the re-install cleanup step for
// Op=Append merged keys: walk the existing manifest record for id (if
// any) and remove each of its Op=Append array elements from the host
// config file before the new plan's appends fire. For Op=Set entries
// the apply path overwrites the leaf, so they're left alone here.
//
// Idempotent across "no existing record" (returns nil without I/O)
// and across "element already gone from the file" (the walker's
// drift-tolerance no-ops). Errors propagate so a corrupted file
// surfaces at the install command rather than mid-write.
//
// Ordering semantics on re-install (hostile-review #5): the un-merge-
// then-append sequence moves the install's elements to the END of the
// target array, even when content is unchanged. If the user installed
// [hookA, hookB] and then re-installs hookA, the array becomes
// [hookB, hookA-new] — hook execution order shifts. This matches
// "re-install = uninstall + install" semantics; users who depend on
// install-order positioning should explicitly uninstall + reinstall
// EVERYTHING in the desired order. Documented here so a future
// reader knows the choice is deliberate, not a bug.
func unmergeExistingAppendsForID(store *manifest.Store, id string) error {
	m, err := store.Load()
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	var rec *manifest.Record
	for i := range m.Installs {
		if m.Installs[i].ID == id {
			rec = &m.Installs[i]
			break
		}
	}
	if rec == nil {
		return nil
	}
	for _, mk := range rec.MergedKeys {
		if adapter.MergedKeyOp(mk.Op) != adapter.MergedKeyAppend {
			continue
		}
		if err := unmergeKey(MergedKeySelector{
			File:     mk.File,
			Path:     mk.Path,
			Op:       adapter.MergedKeyOp(mk.Op),
			Selector: mk.Selector,
		}); err != nil {
			return fmt.Errorf("un-merge existing %s in %s: %w", mk.Path, mk.File, err)
		}
	}
	return nil
}

// preflightMergedKeyCollisions checks whether any merged-key target slot
// is already occupied on disk by a value NOT owned by an existing
// manifest record for this install's ID. Two flavors of collision:
//
//  1. The target file exists and the key path is present, AND no
//     manifest record claims it → user-edited or untracked. Refuse so
//     we don't silently overwrite the user's manual entry.
//  2. The target file exists and the key path is present, AND a
//     DIFFERENT manifest record owns it → install-time collision with
//     another dotpack install. Refuse so we don't silently overwrite
//     someone else's install (the second installer must --force,
//     mirroring the file-collision protection).
//
// Same-ID re-install (rec.ID == id) short-circuits to "no slot
// collisions" — the update path is allowed to overwrite our own
// merged keys. BUT: the symlink check runs FIRST and is NOT scoped
// to first-install. Hook hostile-review finding (this slice): a user
// who replaces settings.json with a symlink between installs would
// otherwise have their symlink silently rewritten by writeAtomic's
// rename — undermining the symlink defense added by the prior
// hostile-review. Symlink defense is a TOCTOU posture against the
// FILESYSTEM, not against a competing manifest record; it must run
// regardless of whether dotpack already owns the merged keys.
func preflightMergedKeyCollisions(store *manifest.Store, id string, mks []adapter.MergedKeyWrite) ([]string, error) {
	m, err := store.Load()
	if err != nil {
		return nil, err
	}
	// Symlink defense runs ALWAYS (before the same-ID short-circuit
	// below) — see docstring. Pre-existing parallel gap on file-drop's
	// preflightCollisions tracked separately; hook slice fixes
	// merged-key only.
	var collisions []string
	for _, mk := range mks {
		if st, lerr := os.Lstat(mk.File); lerr == nil && st.Mode()&os.ModeSymlink != 0 {
			collisions = append(collisions, fmt.Sprintf("%s (symlink — refusing to follow + rename through)", mk.File))
		}
	}
	if len(collisions) > 0 {
		return collisions, nil
	}
	for _, rec := range m.Installs {
		if rec.ID == id {
			return nil, nil
		}
	}
	for _, mk := range mks {
		// Symlink already handled above; the second pass focuses on
		// slot-occupancy checks against the parsed file.
		//
		// Format dispatch by file extension — same convention as
		// applyMergedKey / unmergeKey. A JSON adapter targeting a
		// .toml file (or vice versa) surfaces here at preflight rather
		// than later at apply-time, when half a merged-key plan may
		// already be on disk.
		format, err := formatFromFile(mk.File)
		if err != nil {
			return nil, fmt.Errorf("merged-key preflight: %w", err)
		}
		root, exists, err := readMergeRootForPreflight(mk.File, format)
		if err != nil {
			return nil, fmt.Errorf("merged-key preflight: %w", err)
		}
		if !exists {
			continue
		}
		path, err := parseMergedKeyPath(format, mk.Path)
		if err != nil {
			return nil, fmt.Errorf("merged-key preflight: parse path %q: %w", mk.Path, err)
		}
		switch mk.Op {
		case adapter.MergedKeySet:
			if _, exists := getJSONPath(root, path); exists {
				collisions = append(collisions, fmt.Sprintf("%s#%s", mk.File, mk.Path))
			}
		case adapter.MergedKeyAppend:
			// Op=Append's collision shape is byte-identical sibling
			// rather than slot-occupied. A pre-existing array with
			// OTHER elements is fine — append by definition coexists
			// with siblings. But a pre-existing element with the same
			// content-hash as our new value means another install (or
			// a user-authored entry) already carries this exact
			// binding; appending would duplicate. Refuse so the user
			// can deduplicate or --force-accept the duplicate.
			selector, err := selectorFor(mk.Value)
			if err != nil {
				return nil, fmt.Errorf("merged-key preflight: hash value for %s#%s: %w", mk.File, mk.Path, err)
			}
			leaf, exists := getJSONPath(root, path)
			if !exists {
				continue
			}
			arr, ok := leaf.([]any)
			if !ok {
				// Pre-existing non-array at the array's slot is a
				// genuine collision: append cannot proceed without
				// either coercing the slot to an array (data loss) or
				// refusing. Surface so the user --force's or
				// hand-edits.
				collisions = append(collisions, fmt.Sprintf("%s#%s (existing non-array at array path)", mk.File, mk.Path))
				continue
			}
			for _, el := range arr {
				hash, err := selectorFor(el)
				if err != nil {
					return nil, fmt.Errorf("merged-key preflight: hash existing element at %s#%s: %w", mk.File, mk.Path, err)
				}
				if hash == selector {
					collisions = append(collisions, fmt.Sprintf("%s#%s (byte-identical entry already present — content hash %s)", mk.File, mk.Path, selector))
					break
				}
			}
		}
	}
	return collisions, nil
}

// readMergeRootForPreflight parses path's content into a map[string]any
// for slot-occupancy checking, dispatching on format. Returns (root,
// false, nil) for absent / empty files — preflight treats those as
// "nothing to collide with" rather than errors (the apply step will
// create the file from scratch). Non-map roots (e.g., a user manually
// wrote a JSON array at the top level) return (nil, false, nil) — the
// merged-key walkers all assume a map root, so a non-map root is
// "incompatible with our merge model" which is itself a collision the
// apply step will surface; preflight stays lenient here to avoid
// double-reporting.
func readMergeRootForPreflight(path string, format mergedFormat) (map[string]any, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, false, nil
	}
	var root map[string]any
	switch format {
	case mergedFormatJSON:
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, false, fmt.Errorf("parse %s as JSON: %w", path, err)
		}
	case mergedFormatTOML:
		if err := toml.Unmarshal(raw, &root); err != nil {
			return nil, false, fmt.Errorf("parse %s as TOML: %w", path, err)
		}
	default:
		return nil, false, fmt.Errorf("unknown format %v for %s", format, path)
	}
	if root == nil {
		return nil, false, nil
	}
	return root, true, nil
}

// parseMergedKeyPath dispatches path parsing by format — JSON paths
// require the `$.` prefix; TOML paths use dotted segments only.
func parseMergedKeyPath(format mergedFormat, p string) ([]string, error) {
	switch format {
	case mergedFormatJSON:
		return parseJSONPath(p)
	case mergedFormatTOML:
		return parseTOMLPath(p)
	default:
		return nil, fmt.Errorf("unknown format %v", format)
	}
}
