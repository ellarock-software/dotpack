// Format-aware merged-key apply / un-merge per ADR-0016 §5–§7 + §9.
//
// Why this lives in orchestrator (not adapter): the read-modify-write
// algorithm is FORMAT-specific (JSON vs TOML), not host-specific.
// Adapters emit (file, path, value) tuples in their native path syntax;
// the orchestrator infers format from the file extension and dispatches
// to the right walker. This is the advisor's pushback on ADR-0016 §9's
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
	"os"
	"path/filepath"
	"strings"

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
//     hook's $.hooks.<event> shape per ADR-0016 §9.
func applyMergedKey(mk adapter.MergedKeyWrite) error {
	format, err := formatFromFile(mk.File)
	if err != nil {
		return err
	}
	switch format {
	case mergedFormatJSON:
		return applyJSONMergedKey(mk)
	case mergedFormatTOML:
		// TOML support lands with the codex slice (pelletier/go-toml/v2
		// is the sanctioned dep per ADR-0016 §4). For tracer-bullet
		// shape, the configfrag.New validator rejects FormatTOML
		// policies that would route here; this is a defensive guard.
		return fmt.Errorf("merged-key apply: TOML emit not yet implemented for %s", mk.File)
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
	switch mk.Op {
	case adapter.MergedKeySet:
		if err := deleteJSONPath(root, path); err != nil {
			return fmt.Errorf("merged-key un-merge: delete %q in %s: %w", mk.Path, mk.File, err)
		}
	case adapter.MergedKeyAppend:
		if mk.Selector == "" {
			return fmt.Errorf("merged-key un-merge: %s op=append requires a Selector in the manifest (this is a manifest-shape bug; install should never have persisted an append-MergedKey without one)", mk.Path)
		}
		if err := removeJSONArrayElementBySelector(root, path, mk.Selector); err != nil {
			return fmt.Errorf("merged-key un-merge: remove array element at %q in %s: %w", mk.Path, mk.File, err)
		}
	default:
		return fmt.Errorf("merged-key un-merge: unknown op %q for %s", mk.Op, mk.File)
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
		return fmt.Errorf("merged-key un-merge: TOML emit not yet implemented for %s", mk.File)
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
			return fmt.Errorf("path %v: intermediate segment %q is %T, not a map; refusing to overwrite (manually edit the file or use --force)", path, seg, next)
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
			return fmt.Errorf("path %v: intermediate segment %q is %T, not a map; refusing to overwrite (manually edit the file or use --force)", path, seg, next)
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
		return fmt.Errorf("path %v: leaf segment %q is %T, not an array; refusing to overwrite (manually edit the file or use --force)", path, leafKey, existing)
	}
	cur[leafKey] = append(arr, value)
	return nil
}

// removeJSONArrayElementBySelector navigates to the array at path and
// removes the FIRST element whose sha256 content-hash matches selector.
// Stable across sibling installs/uninstalls and user reorders — the
// hash identity survives where a numeric index would shift.
//
// Drift tolerance: when no element matches (user edited the binding,
// user deleted it, file is missing entirely up to the array), the
// function returns nil and the file is left as-found. This is the
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
func removeJSONArrayElementBySelector(root map[string]any, path []string, selector string) error {
	if len(path) == 0 {
		return fmt.Errorf("removeJSONArrayElementBySelector: empty path")
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		next, ok := cur[seg]
		if !ok {
			return nil
		}
		subMap, ok := next.(map[string]any)
		if !ok {
			return nil
		}
		cur = subMap
	}
	leafKey := path[len(path)-1]
	existing, present := cur[leafKey]
	if !present {
		return nil
	}
	arr, ok := existing.([]any)
	if !ok {
		return nil
	}
	for i, el := range arr {
		hash, err := selectorFor(el)
		if err != nil {
			return fmt.Errorf("hash array element at index %d: %w", i, err)
		}
		if hash == selector {
			cur[leafKey] = append(arr[:i], arr[i+1:]...)
			return nil
		}
	}
	return nil
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

// deleteJSONPath removes root[path[0]][path[1]]...[path[-1]]. Walks the
// path; missing intermediates are a no-op (uninstall is idempotent
// across already-cleaned state).
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
// intentional" (hostile-review #4).
//
// Empty parent maps are NOT recursively removed — see unmergeJSONKey's
// file-retention policy docstring for the rationale.
func deleteJSONPath(root map[string]any, path []string) error {
	if len(path) == 0 {
		return fmt.Errorf("deleteJSONPath: empty path")
	}
	cur := root
	for i := 0; i < len(path)-1; i++ {
		seg := path[i]
		next, ok := cur[seg]
		if !ok {
			return nil
		}
		subMap, ok := next.(map[string]any)
		if !ok {
			return nil
		}
		cur = subMap
	}
	delete(cur, path[len(path)-1])
	return nil
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
		// slot-occupancy checks against the parsed JSON.
		raw, err := os.ReadFile(mk.File)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("merged-key preflight: read %s: %w", mk.File, err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("merged-key preflight: parse %s: %w", mk.File, err)
		}
		if root == nil {
			continue
		}
		path, err := parseJSONPath(mk.Path)
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
