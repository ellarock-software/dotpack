// Config-merge backend registry (ADR-0014).
//
// The read-modify-write for config-fragment merges is FORMAT-specific
// (JSON vs TOML vs YAML), not host-specific — see the header of
// mergedkeys.go. Previously that format dispatch was a hard-coded
// `mergedFormat` enum + switch wired only for .json and .toml. This file
// replaces it with a mergeBackend interface keyed by file extension, so
// adding a config format is implementing the interface + one
// registerBackend call. Every call site (applyMergedKey, unmergeKey,
// preflightMergedKeyCollisions, reconcile) is format-blind: it resolves a
// backend via backendFor(path) and delegates.
//
// The format-agnostic map-walk primitives (setJSONPath, getJSONPath,
// appendJSONPath, deleteJSONPath, removeJSONArrayElementBySelector,
// selectorFor) stay shared free functions in mergedkeys.go — every
// backend reuses them after its own ParsePath splits the path. selectorFor
// stays json.Marshal-based for ALL formats so the append-uninstall content
// hash is format-independent.
package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/ellarock-software/dotpack/internal/adapter"
)

// mergeBackend is the per-format read-modify-write strategy. apply and
// unmerge are the full install / uninstall round-trip for one merged-key
// tuple; readRootForPreflight + parsePath serve the collision-check and
// reconcile paths.
type mergeBackend interface {
	apply(mk adapter.MergedKeyWrite) error
	unmerge(mk MergedKeySelector) error
	// readRootForPreflight parses the file into a map for slot-occupancy
	// checks. Absent / empty / non-map roots return (nil, false, nil) —
	// preflight treats those as "nothing to collide with".
	readRootForPreflight(path string) (map[string]any, bool, error)
	parsePath(p string) ([]string, error)
}

// mergeBackends maps a lower-cased file extension (including the dot) to
// its backend. Populated by init(); extend it via registerBackend.
var mergeBackends = map[string]mergeBackend{}

// registerBackend wires a backend for ext (e.g. ".json"). Panics on a
// duplicate — two backends claiming one extension is a programmer error.
func registerBackend(ext string, b mergeBackend) {
	if _, dup := mergeBackends[ext]; dup {
		panic("orchestrator: duplicate merge backend for extension " + ext)
	}
	mergeBackends[ext] = b
}

// backendFor resolves the merge backend for a target file by extension.
// Unknown extensions return a structured error (preserving the
// "unsupported file extension" substring existing callers/tests rely on)
// rather than defaulting — silently treating an unknown extension as JSON
// would garble TOML/YAML and vice versa.
func backendFor(path string) (mergeBackend, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if b, ok := mergeBackends[ext]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("merged-key apply: unsupported file extension for %s (no merge backend registered; shipped: %s)", path, registeredExtensions())
}

// registeredExtensions returns the sorted set of wired extensions for
// error messages.
func registeredExtensions() string {
	exts := make([]string, 0, len(mergeBackends))
	for e := range mergeBackends {
		exts = append(exts, e)
	}
	// small set; insertion sort keeps determinism without importing sort
	for i := 1; i < len(exts); i++ {
		for j := i; j > 0 && exts[j] < exts[j-1]; j-- {
			exts[j], exts[j-1] = exts[j-1], exts[j]
		}
	}
	return strings.Join(exts, ", ")
}

func init() {
	registerBackend(".json", jsonBackend{})
	registerBackend(".toml", tomlBackend{})
	registerBackend(".yaml", yamlBackend{})
	registerBackend(".yml", yamlBackend{})
}

// readRootWith is the shared read-into-map for preflight / unmerge across
// formats. Absent file → (nil,false,nil); empty / whitespace-only →
// (nil,false,nil); a parse error → error; nil root → (nil,false,nil).
// This is the former readMergeRootForPreflight body, parameterised by the
// format's unmarshal so each backend supplies its own.
func readRootWith(path string, unmarshal func([]byte, any) error, formatName string) (map[string]any, bool, error) {
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
	if err := unmarshal(raw, &root); err != nil {
		return nil, false, fmt.Errorf("parse %s as %s: %w", path, formatName, err)
	}
	if root == nil {
		return nil, false, nil
	}
	return root, true, nil
}

// jsonBackend wraps the JSON read-modify-write functions in mergedkeys.go.
type jsonBackend struct{}

func (jsonBackend) apply(mk adapter.MergedKeyWrite) error { return applyJSONMergedKey(mk) }
func (jsonBackend) unmerge(mk MergedKeySelector) error    { return unmergeJSONKey(mk) }
func (jsonBackend) readRootForPreflight(path string) (map[string]any, bool, error) {
	return readRootWith(path, json.Unmarshal, "JSON")
}
func (jsonBackend) parsePath(p string) ([]string, error) { return parseJSONPath(p) }

// tomlBackend wraps the TOML read-modify-write functions in mergedkeys.go.
type tomlBackend struct{}

func (tomlBackend) apply(mk adapter.MergedKeyWrite) error { return applyTOMLMergedKey(mk) }
func (tomlBackend) unmerge(mk MergedKeySelector) error    { return unmergeTOMLKey(mk) }
func (tomlBackend) readRootForPreflight(path string) (map[string]any, bool, error) {
	return readRootWith(path, toml.Unmarshal, "TOML")
}
func (tomlBackend) parsePath(p string) ([]string, error) { return parseTOMLPath(p) }

// yamlBackend is the real third format proving the set is open (ADR-0014).
// No adapter targets YAML today; it exists so a future host whose config
// is YAML (and the openness tests) have a working backend with zero
// changes to dispatch.
//
// yaml.v3 decodes a normal (all-string-key) mapping into map[string]any
// throughout, so the shared format-agnostic map-walk primitives apply
// unchanged. A non-string key at a NESTED level (e.g. `1: x`) decodes to
// map[any]any at that level only; such a sibling is left untouched and
// round-trips byte-stable (the walkers only descend the path being
// merged, never siblings). A dotpack-merged path is always a string
// identifier, so it never descends into such a node; the pathological
// case where it would (a host config whose dotpack-merged subtree uses
// non-string keys) surfaces a clear walker error and is recorded in the
// ADR-0014 gap register rather than silently coerced — coercion would
// rewrite `1: x` to `"1": x` and mutate bytes dotpack does not own. Like
// JSON, YAML does no value normalization (TOML's int-vs-float coercion is
// TOML-specific).
type yamlBackend struct{}

func (yamlBackend) apply(mk adapter.MergedKeyWrite) error { return applyYAMLMergedKey(mk) }
func (yamlBackend) unmerge(mk MergedKeySelector) error    { return unmergeYAMLKey(mk) }
func (yamlBackend) readRootForPreflight(path string) (map[string]any, bool, error) {
	return readRootWith(path, yaml.Unmarshal, "YAML")
}
func (yamlBackend) parsePath(p string) ([]string, error) { return parseYAMLPath(p) }

// parseYAMLPath splits a dotted YAML path ("mcp.foo") into segments.
// Mirrors parseTOMLPath: no leading `$` (that is JSON syntax), empty path
// / empty segments rejected. A JSON-syntax path mistakenly emitted into a
// YAML target surfaces here rather than creating a literal `$` key.
func parseYAMLPath(p string) ([]string, error) {
	if p == "" {
		return nil, fmt.Errorf("YAML path is empty (root-only paths cannot be merged into)")
	}
	if strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("YAML path must NOT have $ prefix; got %q (JSON-syntax path passed to YAML walker — adapter emit bug)", p)
	}
	parts := strings.Split(p, ".")
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("YAML path %q has an empty segment at position %d", p, i)
		}
	}
	return parts, nil
}

// readYAMLOrEmpty mirrors readJSONOrEmpty: absent / empty → empty map;
// non-mapping root → structured error.
func readYAMLOrEmpty(path string) (map[string]any, error) {
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
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse %s as YAML object: %w", path, err)
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

// writeYAML serialises root → bytes and atomic-writes, mirroring
// writeJSON/writeTOML mode preservation. yaml.v3 emits map keys in sorted
// order, so output is deterministic for diffing.
func writeYAML(path string, root map[string]any) error {
	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal %s as YAML: %w", path, err)
	}
	mode := preserveMode(path, 0o644)
	return writeAtomic(adapter.FileWrite{Path: path, Content: out, Mode: mode})
}

// applyYAMLMergedKey mirrors applyJSONMergedKey (no TOML-style value
// normalization — YAML round-trips numbers like JSON does at the
// map[string]any level for the shapes dotpack emits).
func applyYAMLMergedKey(mk adapter.MergedKeyWrite) error {
	root, err := readYAMLOrEmpty(mk.File)
	if err != nil {
		return err
	}
	path, err := parseYAMLPath(mk.Path)
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
	return writeYAML(mk.File, root)
}

// unmergeYAMLKey mirrors unmergeJSONKey: absent/empty/nil root → no-op;
// no-op-no-write preserves user bytes on idempotent uninstall.
func unmergeYAMLKey(mk MergedKeySelector) error {
	root, exists, err := readRootWith(mk.File, yaml.Unmarshal, "YAML")
	if err != nil {
		return fmt.Errorf("merged-key un-merge: %w", err)
	}
	if !exists {
		return nil
	}
	path, err := parseYAMLPath(mk.Path)
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
	if !changed {
		return nil
	}
	return writeYAML(mk.File, root)
}
