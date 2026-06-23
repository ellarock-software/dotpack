package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

func TestMergedKeyExistsAppendAndErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	value := map[string]any{"matcher": "Bash"}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: path, Path: "$.hooks.PreToolUse", Value: value, Op: adapter.MergedKeyAppend}); err != nil {
		t.Fatalf("apply append: %v", err)
	}
	selector, err := selectorFor(value)
	if err != nil {
		t.Fatalf("selectorFor: %v", err)
	}
	exists, err := mergedKeyExists(manifest.MergedKey{File: path, Path: "$.hooks.PreToolUse", Op: string(adapter.MergedKeyAppend), Selector: selector})
	if err != nil || !exists {
		t.Fatalf("mergedKeyExists append = %v,%v; want true,nil", exists, err)
	}
	exists, err = mergedKeyExists(manifest.MergedKey{File: path, Path: "$.hooks.PreToolUse", Op: string(adapter.MergedKeyAppend), Selector: "sha256:nope"})
	if err != nil || exists {
		t.Fatalf("mergedKeyExists missing append = %v,%v; want false,nil", exists, err)
	}
	if _, err := mergedKeyExists(manifest.MergedKey{File: path, Path: "$.hooks.PreToolUse", Op: string(adapter.MergedKeyAppend)}); err == nil || !strings.Contains(err.Error(), "requires selector") {
		t.Fatalf("missing selector error = %v", err)
	}
	if _, err := mergedKeyExists(manifest.MergedKey{File: path, Path: "$.hooks.PreToolUse", Op: "bad"}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("unknown op error = %v", err)
	}
	if _, err := mergedKeyExists(manifest.MergedKey{File: path, Path: "bad", Op: ""}); err == nil || !strings.Contains(err.Error(), "parse path") {
		t.Fatalf("bad path error = %v", err)
	}
}

func TestMoreJSONAndTOMLWalkerBranches(t *testing.T) {
	root := map[string]any{"a": map[string]any{"b": []any{map[string]any{"x": "y"}}}}
	if got, ok := getJSONPath(root, []string{"a", "b"}); !ok || got == nil {
		t.Fatalf("getJSONPath existing = %v,%v", got, ok)
	}
	if got, ok := getJSONPath(root, []string{"a", "missing"}); ok || got != nil {
		t.Fatalf("getJSONPath missing = %v,%v", got, ok)
	}
	if changed, err := deleteJSONPath(map[string]any{"a": "not-map"}, []string{"a", "b"}); err != nil || changed {
		t.Fatalf("deleteJSONPath non-map changed=%v err=%v; want no-op", changed, err)
	}
	if changed, err := deleteJSONPath(root, []string{"a", "missing"}); err != nil || changed {
		t.Fatalf("deleteJSONPath missing changed=%v err=%v", changed, err)
	}
	if _, err := parseMergedKeyPath(mergedFormatTOML, "$.bad"); err == nil || !strings.Contains(err.Error(), "must NOT") {
		t.Fatalf("parseMergedKeyPath TOML json-style err=%v", err)
	}

	tmp := t.TempDir()
	tomlPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(tomlPath, []byte("[hooks]\nPreToolUse = \"not-array\"\n"), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: tomlPath, Path: "hooks.PreToolUse", Op: adapter.MergedKeyAppend, Selector: "sha256:nope"}); err != nil {
		t.Fatalf("unmergeTOML append non-array should no-op: %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: tomlPath, Path: "hooks.PreToolUse", Op: adapter.MergedKeyAppend}); err == nil || !strings.Contains(err.Error(), "Selector") {
		t.Fatalf("unmergeTOML missing selector err=%v", err)
	}
}
