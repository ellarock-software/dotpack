package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ellarock-software/dotpack/internal/adapter"
)

// withBackend installs b for ext for the duration of the test, restoring
// the prior state via t.Cleanup. Each caller MUST use a unique extension
// so concurrent tests never race on the shared mergeBackends map.
func withBackend(t *testing.T, ext string, b mergeBackend) {
	t.Helper()
	old, had := mergeBackends[ext]
	mergeBackends[ext] = b
	t.Cleanup(func() {
		if had {
			mergeBackends[ext] = old
		} else {
			delete(mergeBackends, ext)
		}
	})
}

// spyBackend records which methods fired, proving backendFor dispatches
// purely on extension with no hard-coded format switch.
type spyBackend struct {
	applied   bool
	unmerged  bool
	preflight bool
	parsed    bool
}

func (s *spyBackend) apply(mk adapter.MergedKeyWrite) error { s.applied = true; return nil }
func (s *spyBackend) unmerge(mk MergedKeySelector) error    { s.unmerged = true; return nil }
func (s *spyBackend) readRootForPreflight(path string) (map[string]any, bool, error) {
	s.preflight = true
	return nil, false, nil
}
func (s *spyBackend) parsePath(p string) ([]string, error) {
	s.parsed = true
	return strings.Split(p, "."), nil
}

// TestFakeMergeBackendDispatch proves a brand-new config format onboards
// by registering one backend — applyMergedKey/unmergeKey route to it
// purely on the file extension, no core edit (ADR-0014).
func TestFakeMergeBackendDispatch(t *testing.T) {
	spy := &spyBackend{}
	withBackend(t, ".fakefmt", spy)

	file := filepath.Join(t.TempDir(), "config.fakefmt")
	if err := applyMergedKey(adapter.MergedKeyWrite{File: file, Path: "a.b", Value: "x"}); err != nil {
		t.Fatalf("apply via fake backend: %v", err)
	}
	if !spy.applied {
		t.Fatal("apply did not dispatch to the fake backend")
	}
	if err := unmergeKey(MergedKeySelector{File: file, Path: "a.b"}); err != nil {
		t.Fatalf("unmerge via fake backend: %v", err)
	}
	if !spy.unmerged {
		t.Fatal("unmerge did not dispatch to the fake backend")
	}
}

// TestYAMLBackendRoundTrip exercises the real YAML backend end-to-end:
// set + append apply, read-back, idempotent re-install, and clean unmerge.
func TestYAMLBackendRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")

	// Set a nested leaf.
	set := adapter.MergedKeyWrite{File: file, Path: "mcp.github", Value: map[string]any{"command": "gh"}}
	if err := applyMergedKey(set); err != nil {
		t.Fatalf("apply set: %v", err)
	}
	// Append into an array.
	app := adapter.MergedKeyWrite{File: file, Path: "hooks.PreToolUse", Value: map[string]any{"matcher": "Bash"}, Op: adapter.MergedKeyAppend}
	if err := applyMergedKey(app); err != nil {
		t.Fatalf("apply append: %v", err)
	}

	root := readYAMLFile(t, file)
	mcp, _ := root["mcp"].(map[string]any)
	gh, _ := mcp["github"].(map[string]any)
	if gh["command"] != "gh" {
		t.Fatalf("yaml set leaf = %#v; want command: gh", root["mcp"])
	}
	hooks, _ := root["hooks"].(map[string]any)
	arr, _ := hooks["PreToolUse"].([]any)
	if len(arr) != 1 {
		t.Fatalf("yaml append array = %#v; want 1 element", hooks["PreToolUse"])
	}

	// Idempotent re-install of the set leaf must not error or duplicate.
	if err := applyMergedKey(set); err != nil {
		t.Fatalf("re-apply set: %v", err)
	}

	// Unmerge the set leaf; the array stays.
	sel := MergedKeySelector{File: file, Path: "mcp.github", Op: adapter.MergedKeySet}
	if err := unmergeKey(sel); err != nil {
		t.Fatalf("unmerge set: %v", err)
	}
	root = readYAMLFile(t, file)
	if mcp, _ := root["mcp"].(map[string]any); len(mcp) != 0 {
		t.Fatalf("yaml leaf survived unmerge: %#v", root["mcp"])
	}
}

// TestYAMLNestedStringKeysWalk pins that yaml.v3 decodes a normal
// all-string-key mapping into map[string]any throughout, so the shared
// format-agnostic walkers descend a nested string path. If a future
// yaml.v3 changed this, the YAML merge path would fail the type
// assertions — this test fails first, pinpointing the cause.
func TestYAMLNestedStringKeysWalk(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	set := adapter.MergedKeyWrite{File: file, Path: "a.b.c", Value: "deep"}
	if err := applyMergedKey(set); err != nil {
		t.Fatalf("apply nested: %v", err)
	}
	root := readYAMLFile(t, file)
	a, _ := root["a"].(map[string]any)
	b, _ := a["b"].(map[string]any)
	if b["c"] != "deep" {
		t.Fatalf("nested string-keyed walk failed: %#v", root)
	}
}

// TestYAMLNonStringKeyedSiblingPreserved documents the backend's actual
// (and safest) behavior with the pathological non-string-keyed case: a
// merge whose path does NOT descend into a non-string-keyed map leaves
// that sibling untouched and byte-stable — coercing it to string keys
// would mutate bytes dotpack does not own (`1: x` -> `"1": x`). The
// pathological case (a dotpack-merged path that DOES descend into such a
// node) is recorded in the ADR-0014 gap register, not silently coerced.
func TestYAMLNonStringKeyedSiblingPreserved(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("ports:\n  1: alpha\n  2: beta\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	set := adapter.MergedKeyWrite{File: file, Path: "mcp.github", Value: map[string]any{"command": "gh"}}
	if err := applyMergedKey(set); err != nil {
		t.Fatalf("apply alongside non-string-keyed sibling: %v", err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), "1: alpha") || !strings.Contains(string(raw), "2: beta") {
		t.Fatalf("non-string-keyed sibling not preserved byte-stable:\n%s", raw)
	}
	if !strings.Contains(string(raw), "github") {
		t.Fatalf("merge leaf missing:\n%s", raw)
	}
}

func readYAMLFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return root
}
