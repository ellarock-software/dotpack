package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
)

func TestMergedKeyAdditionalJSONBranches(t *testing.T) {
	tmp := t.TempDir()

	if err := applyJSONMergedKey(adapter.MergedKeyWrite{File: tmp, Path: "$.x", Value: "y"}); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("applyJSONMergedKey directory err=%v; want read", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: tmp, Path: "$.x"}); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("unmergeJSONKey directory err=%v; want read", err)
	}

	emptyJSON := filepath.Join(tmp, "empty.json")
	if err := os.WriteFile(emptyJSON, []byte("  \n"), 0o644); err != nil {
		t.Fatalf("write empty json: %v", err)
	}
	root, err := readJSONOrEmpty(emptyJSON)
	if err != nil || len(root) != 0 {
		t.Fatalf("readJSONOrEmpty whitespace = %#v err=%v; want empty", root, err)
	}

	nullJSON := filepath.Join(tmp, "null-root.json")
	if err := os.WriteFile(nullJSON, []byte("null"), 0o644); err != nil {
		t.Fatalf("write null json: %v", err)
	}
	root, err = readJSONOrEmpty(nullJSON)
	if err != nil || len(root) != 0 {
		t.Fatalf("readJSONOrEmpty null = %#v err=%v; want empty", root, err)
	}

	appendLeaf := filepath.Join(tmp, "append-leaf.json")
	if err := os.WriteFile(appendLeaf, []byte(`{"hooks":{"PreToolUse":{}}}`), 0o644); err != nil {
		t.Fatalf("write append leaf json: %v", err)
	}
	if err := applyJSONMergedKey(adapter.MergedKeyWrite{File: appendLeaf, Path: "$.hooks.PreToolUse", Value: "x", Op: adapter.MergedKeyAppend}); err == nil || !strings.Contains(err.Error(), "not an array") {
		t.Fatalf("applyJSONMergedKey append leaf err=%v; want not array", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: appendLeaf, Path: "$.missing"}); err != nil {
		t.Fatalf("unmergeJSONKey missing path should no-op: %v", err)
	}
}

func TestMergedKeyAdditionalTOMLBranches(t *testing.T) {
	tmp := t.TempDir()

	if err := applyTOMLMergedKey(adapter.MergedKeyWrite{File: tmp, Path: "x", Value: "y"}); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("applyTOMLMergedKey directory err=%v; want read", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: tmp, Path: "x"}); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("unmergeTOMLKey directory err=%v; want read", err)
	}

	emptyTOML := filepath.Join(tmp, "empty.toml")
	if err := os.WriteFile(emptyTOML, []byte("  \n"), 0o644); err != nil {
		t.Fatalf("write empty toml: %v", err)
	}
	root, err := readTOMLOrEmpty(emptyTOML)
	if err != nil || len(root) != 0 {
		t.Fatalf("readTOMLOrEmpty whitespace = %#v err=%v; want empty", root, err)
	}

	badTOML := filepath.Join(tmp, "bad-unmerge.toml")
	if err := os.WriteFile(badTOML, []byte("[bad"), 0o644); err != nil {
		t.Fatalf("write bad toml: %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: badTOML, Path: "x"}); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("unmergeTOMLKey bad toml err=%v; want parse", err)
	}

	setNonMap := filepath.Join(tmp, "set-non-map.toml")
	if err := os.WriteFile(setNonMap, []byte("mcp_servers = 'not-map'\n"), 0o644); err != nil {
		t.Fatalf("write set non-map toml: %v", err)
	}
	if err := applyTOMLMergedKey(adapter.MergedKeyWrite{File: setNonMap, Path: "mcp_servers.github", Value: "x"}); err == nil || !strings.Contains(err.Error(), "not a map") {
		t.Fatalf("applyTOMLMergedKey set non-map err=%v; want not a map", err)
	}

	appendNormalize := filepath.Join(tmp, "append-normalize.toml")
	if err := applyTOMLMergedKey(adapter.MergedKeyWrite{File: appendNormalize, Path: "hooks.PreToolUse", Value: []any{nil}, Op: adapter.MergedKeyAppend}); err == nil || !strings.Contains(err.Error(), "nil array element") {
		t.Fatalf("applyTOMLMergedKey append normalize err=%v; want nil array element", err)
	}

	appendLeaf := filepath.Join(tmp, "append-leaf.toml")
	if err := os.WriteFile(appendLeaf, []byte("[hooks]\nPreToolUse = 'not-array'\n"), 0o644); err != nil {
		t.Fatalf("write append leaf toml: %v", err)
	}
	if err := applyTOMLMergedKey(adapter.MergedKeyWrite{File: appendLeaf, Path: "hooks.PreToolUse", Value: "x", Op: adapter.MergedKeyAppend}); err == nil || !strings.Contains(err.Error(), "not an array") {
		t.Fatalf("applyTOMLMergedKey append leaf err=%v; want not array", err)
	}
}

func TestMergedKeyAdditionalWalkerNoOps(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": map[string]any{}}}

	if err := setJSONPath(root, []string{"a", "b", "c"}, "v"); err != nil {
		t.Fatalf("setJSONPath existing map path: %v", err)
	}
	if got, ok := getJSONPath(root, nil); ok || got != nil {
		t.Fatalf("getJSONPath empty = %v,%v; want nil,false", got, ok)
	}
	if got, ok := getJSONPath(map[string]any{"a": "not-map"}, []string{"a", "b"}); ok || got != nil {
		t.Fatalf("getJSONPath non-map = %v,%v; want nil,false", got, ok)
	}
	if err := appendJSONPath(root, nil, "x"); err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("appendJSONPath empty err=%v; want empty path", err)
	}

	if changed, err := removeJSONArrayElementBySelector(map[string]any{}, []string{"a", "b"}, "sha256:nope"); err != nil || changed {
		t.Fatalf("remove missing intermediate changed=%v err=%v", changed, err)
	}
	if changed, err := removeJSONArrayElementBySelector(map[string]any{"a": "not-map"}, []string{"a", "b"}, "sha256:nope"); err != nil || changed {
		t.Fatalf("remove non-map intermediate changed=%v err=%v", changed, err)
	}
	if changed, err := removeJSONArrayElementBySelector(map[string]any{"a": map[string]any{}}, []string{"a", "b"}, "sha256:nope"); err != nil || changed {
		t.Fatalf("remove missing leaf changed=%v err=%v", changed, err)
	}
	if changed, err := removeJSONArrayElementBySelector(map[string]any{"a": []any{"x"}}, []string{"a"}, "sha256:nope"); err != nil || changed {
		t.Fatalf("remove no matching selector changed=%v err=%v", changed, err)
	}

	if changed, err := deleteJSONPath(map[string]any{}, []string{"a", "b"}); err != nil || changed {
		t.Fatalf("delete missing intermediate changed=%v err=%v", changed, err)
	}
}

func TestReadMergeRootAdditionalBranches(t *testing.T) {
	tmp := t.TempDir()

	jsonB, err := backendFor("x.json")
	if err != nil {
		t.Fatalf("backendFor json: %v", err)
	}
	dirJSON := filepath.Join(tmp, "adir.json")
	if err := os.Mkdir(dirJSON, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, _, err := jsonB.readRootForPreflight(dirJSON); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("readRootForPreflight directory err=%v; want read", err)
	}

	nullJSON := filepath.Join(tmp, "null.json")
	if err := os.WriteFile(nullJSON, []byte("null"), 0o644); err != nil {
		t.Fatalf("write null json: %v", err)
	}
	root, exists, err := jsonB.readRootForPreflight(nullJSON)
	if err != nil || exists || root != nil {
		t.Fatalf("readRootForPreflight null root=%v exists=%v err=%v; want nil,false,nil", root, exists, err)
	}
}
