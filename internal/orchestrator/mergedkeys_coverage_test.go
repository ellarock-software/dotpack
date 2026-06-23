package orchestrator

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

func TestApplyAndUnmergeJSONSetAndAppend(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "settings.json")
	set := adapter.MergedKeyWrite{
		File: path,
		Path: "$.mcpServers.github",
		Value: map[string]any{
			"command": "npx",
			"args":    []any{"-y", "github"},
		},
	}
	if err := applyMergedKey(set); err != nil {
		t.Fatalf("apply set: %v", err)
	}
	appendValue := map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "/bin/true"}}}
	appendMK := adapter.MergedKeyWrite{File: path, Path: "$.hooks.PreToolUse", Value: appendValue, Op: adapter.MergedKeyAppend}
	if err := applyMergedKey(appendMK); err != nil {
		t.Fatalf("apply append: %v", err)
	}

	raw := mustReadBytes(t, path)
	if !strings.Contains(string(raw), `"mcpServers"`) || !strings.Contains(string(raw), `"PreToolUse"`) {
		t.Fatalf("merged JSON missing expected keys:\n%s", raw)
	}
	selector, err := selectorFor(appendValue)
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	if err := unmergeKey(MergedKeySelector{File: path, Path: "$.hooks.PreToolUse", Op: adapter.MergedKeyAppend, Selector: selector}); err != nil {
		t.Fatalf("unmerge append: %v", err)
	}
	if err := unmergeKey(MergedKeySelector{File: path, Path: "$.mcpServers.github"}); err != nil {
		t.Fatalf("unmerge set: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(mustReadBytes(t, path), &root); err != nil {
		t.Fatalf("parse after unmerge: %v", err)
	}
	if _, exists := getJSONPath(root, []string{"mcpServers", "github"}); exists {
		t.Fatalf("set leaf survived unmerge: %#v", root)
	}
}

func TestMergedKeyApplyAndUnmergeErrors(t *testing.T) {
	tmp := t.TempDir()
	if err := applyMergedKey(adapter.MergedKeyWrite{File: filepath.Join(tmp, "x.yaml"), Path: "$.x", Value: "y"}); err == nil || !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("unsupported apply error = %v", err)
	}
	if err := unmergeKey(MergedKeySelector{File: filepath.Join(tmp, "x.yaml"), Path: "$.x"}); err == nil || !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("unsupported unmerge error = %v", err)
	}

	jsonPath := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(jsonPath, []byte(`{"mcpServers":"not-map"}`), 0o644); err != nil {
		t.Fatalf("seed json: %v", err)
	}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: jsonPath, Path: "$.mcpServers.github", Value: "x"}); err == nil || !strings.Contains(err.Error(), "not a map") {
		t.Fatalf("non-map apply error = %v", err)
	}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: jsonPath, Path: "mcpServers.github", Value: "x"}); err == nil || !strings.Contains(err.Error(), "must start with $") {
		t.Fatalf("bad JSON path error = %v", err)
	}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: jsonPath, Path: "$.x", Value: "y", Op: adapter.MergedKeyOp("bad")}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("unknown op apply error = %v", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: jsonPath, Path: "$.hooks.PreToolUse", Op: adapter.MergedKeyAppend}); err == nil || !strings.Contains(err.Error(), "Selector") {
		t.Fatalf("missing selector error = %v", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: jsonPath, Path: "$.x", Op: adapter.MergedKeyOp("bad")}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("unknown op unmerge error = %v", err)
	}

	arrPath := filepath.Join(tmp, "array.json")
	if err := os.WriteFile(arrPath, []byte(`[]`), 0o644); err != nil {
		t.Fatalf("seed array: %v", err)
	}
	if _, err := readJSONOrEmpty(arrPath); err == nil {
		t.Fatal("readJSONOrEmpty should reject non-object roots")
	}
}

func TestAppendJSONPathErrorsAndNoOps(t *testing.T) {
	root := map[string]any{"hooks": map[string]any{"PreToolUse": map[string]any{}}}
	if err := appendJSONPath(root, []string{"hooks", "PreToolUse"}, "x"); err == nil || !strings.Contains(err.Error(), "not an array") {
		t.Fatalf("append leaf error = %v", err)
	}
	if changed, err := removeJSONArrayElementBySelector(root, []string{"hooks", "PreToolUse"}, "sha256:nope"); err != nil || changed {
		t.Fatalf("remove non-array should no-op, changed=%v err=%v", changed, err)
	}
	if _, err := removeJSONArrayElementBySelector(root, nil, "x"); err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("empty remove error = %v", err)
	}
	if _, err := deleteJSONPath(root, nil); err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("empty delete error = %v", err)
	}
	if err := setJSONPath(root, nil, "x"); err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Fatalf("empty set error = %v", err)
	}
}

func TestPreflightMergedKeyCollisions(t *testing.T) {
	tmp := t.TempDir()
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	rec := manifest.Record{ID: "host:mcp-server:github", Scope: "user", TargetRoot: tmp}
	path := filepath.Join(tmp, "settings.json")

	mks := []adapter.MergedKeyWrite{{File: path, Path: "$.mcpServers.github", Value: map[string]any{"command": "npx"}}}
	collisions, err := preflightMergedKeyCollisions(store, rec, mks)
	if err != nil || len(collisions) != 0 {
		t.Fatalf("absent file collisions=%v err=%v", collisions, err)
	}

	if err := os.WriteFile(path, []byte(`{"mcpServers":{"github":{"command":"manual"}}}`), 0o644); err != nil {
		t.Fatalf("seed occupied: %v", err)
	}
	collisions, err = preflightMergedKeyCollisions(store, rec, mks)
	if err != nil || len(collisions) != 1 || !strings.Contains(collisions[0], "#$.mcpServers.github") {
		t.Fatalf("set collision=%v err=%v", collisions, err)
	}

	dupValue := map[string]any{"matcher": "Bash"}
	if err := os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash"}]}}`), 0o644); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	collisions, err = preflightMergedKeyCollisions(store, rec, []adapter.MergedKeyWrite{{File: path, Path: "$.hooks.PreToolUse", Value: dupValue, Op: adapter.MergedKeyAppend}})
	if err != nil || len(collisions) != 1 || !strings.Contains(collisions[0], "byte-identical") {
		t.Fatalf("append duplicate collision=%v err=%v", collisions, err)
	}

	if err := os.WriteFile(path, []byte(`{"hooks":{"PreToolUse":{}}}`), 0o644); err != nil {
		t.Fatalf("seed non-array: %v", err)
	}
	collisions, err = preflightMergedKeyCollisions(store, rec, []adapter.MergedKeyWrite{{File: path, Path: "$.hooks.PreToolUse", Value: dupValue, Op: adapter.MergedKeyAppend}})
	if err != nil || len(collisions) != 1 || !strings.Contains(collisions[0], "existing non-array") {
		t.Fatalf("append non-array collision=%v err=%v", collisions, err)
	}

	symlink := filepath.Join(tmp, "link.json")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	collisions, err = preflightMergedKeyCollisions(store, rec, []adapter.MergedKeyWrite{{File: symlink, Path: "$.x", Value: "y"}})
	if err != nil || len(collisions) != 1 || !strings.Contains(collisions[0], "symlink") {
		t.Fatalf("symlink collision=%v err=%v", collisions, err)
	}
}

func TestTOMLSetDispatchAndErrors(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	mk := adapter.MergedKeyWrite{File: path, Path: "mcp_servers.github", Value: map[string]any{"command": "npx", "timeout": float64(30)}}
	if err := applyMergedKey(mk); err != nil {
		t.Fatalf("apply TOML set: %v", err)
	}
	raw := string(mustReadBytes(t, path))
	if !strings.Contains(raw, "[mcp_servers.github]") || !strings.Contains(raw, "timeout = 30") {
		t.Fatalf("unexpected TOML:\n%s", raw)
	}
	if err := unmergeKey(MergedKeySelector{File: path, Path: "mcp_servers.github"}); err != nil {
		t.Fatalf("unmerge TOML set: %v", err)
	}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: path, Path: "$.bad", Value: "x"}); err == nil || !strings.Contains(err.Error(), "must NOT have $ prefix") {
		t.Fatalf("bad TOML path error = %v", err)
	}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: path, Path: "x", Value: []any{nil}}); err == nil || !strings.Contains(err.Error(), "nil array element") {
		t.Fatalf("normalize error = %v", err)
	}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: path, Path: "x", Value: "y", Op: adapter.MergedKeyOp("bad")}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("unknown TOML op = %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: path, Path: "x", Op: adapter.MergedKeyOp("bad")}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("unknown TOML unmerge op = %v", err)
	}
}

func TestNormalizeForTOMLNestedErrors(t *testing.T) {
	if _, err := normalizeForTOML(math.Inf(1)); err == nil || !strings.Contains(err.Error(), "non-finite") {
		t.Fatalf("non-finite error = %v", err)
	}
	if _, err := normalizeForTOML(map[string]any{"nested": []any{math.NaN()}}); err == nil || !strings.Contains(err.Error(), ".nested") {
		t.Fatalf("nested normalize error = %v", err)
	}
}

func TestReadMergeRootForPreflight(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "missing.json")
	if root, exists, err := readMergeRootForPreflight(missing, mergedFormatJSON); err != nil || exists || root != nil {
		t.Fatalf("missing root=%v exists=%v err=%v", root, exists, err)
	}
	empty := filepath.Join(tmp, "empty.toml")
	if err := os.WriteFile(empty, []byte("  \n"), 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if root, exists, err := readMergeRootForPreflight(empty, mergedFormatTOML); err != nil || exists || root != nil {
		t.Fatalf("empty root=%v exists=%v err=%v", root, exists, err)
	}
	bad := filepath.Join(tmp, "bad.toml")
	if err := os.WriteFile(bad, []byte("[bad"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, _, err := readMergeRootForPreflight(bad, mergedFormatTOML); err == nil {
		t.Fatal("expected TOML parse error")
	}
	unknown := filepath.Join(tmp, "unknown.any")
	if err := os.WriteFile(unknown, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatalf("write unknown: %v", err)
	}
	if _, _, err := readMergeRootForPreflight(unknown, mergedFormat(99)); err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("unknown format error = %v", err)
	}
}

func mustReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}
