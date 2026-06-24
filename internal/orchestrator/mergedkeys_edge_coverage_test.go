package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

func TestMergedKeyReadWriteParseAndSelectorErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	if _, err := readJSONOrEmpty(tmp); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("readJSONOrEmpty directory err=%v; want read", err)
	}
	if err := writeJSON(filepath.Join(tmp, "bad.json"), map[string]any{"bad": make(chan int)}); err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("writeJSON marshal err=%v; want marshal", err)
	}
	if _, err := readTOMLOrEmpty(tmp); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("readTOMLOrEmpty directory err=%v; want read", err)
	}
	badToml := filepath.Join(tmp, "bad.toml")
	if err := os.WriteFile(badToml, []byte("[bad"), 0o644); err != nil {
		t.Fatalf("write bad toml: %v", err)
	}
	if _, err := readTOMLOrEmpty(badToml); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("readTOMLOrEmpty parse err=%v; want parse", err)
	}
	if err := writeTOML(filepath.Join(tmp, "bad-out.toml"), map[string]any{"bad": make(chan int)}); err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("writeTOML marshal err=%v; want marshal", err)
	}

	for _, path := range []string{"$", "$bad", "$.", "$.a..b"} {
		if _, err := parseJSONPath(path); err == nil {
			t.Fatalf("parseJSONPath(%q) expected error", path)
		}
	}
	for _, path := range []string{"", "a..b"} {
		if _, err := parseTOMLPath(path); err == nil {
			t.Fatalf("parseTOMLPath(%q) expected error", path)
		}
	}
	// Path parsing is now per-backend (ADR-0014); an unknown extension is
	// rejected by backendFor before any parse happens.
	if _, err := backendFor("config.ini"); err == nil || !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("backendFor unknown ext err=%v; want unsupported file extension", err)
	}

	root := map[string]any{"hooks": map[string]any{"PreToolUse": []any{make(chan int)}}}
	if _, err := removeJSONArrayElementBySelector(root, []string{"hooks", "PreToolUse"}, "sha256:nope"); err == nil || !strings.Contains(err.Error(), "hash array element") {
		t.Fatalf("removeJSONArrayElementBySelector hash err=%v; want hash array element", err)
	}
	if err := appendJSONPath(map[string]any{"hooks": "not-map"}, []string{"hooks", "PreToolUse"}, "x"); err == nil || !strings.Contains(err.Error(), "not a map") {
		t.Fatalf("appendJSONPath non-map err=%v; want not a map", err)
	}
}

func TestUnmergeJSONAndTOMLNoOpAndErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	missingJSON := filepath.Join(tmp, "missing.json")
	if err := unmergeJSONKey(MergedKeySelector{File: missingJSON, Path: "$.x"}); err != nil {
		t.Fatalf("unmerge missing JSON should no-op: %v", err)
	}
	emptyJSON := filepath.Join(tmp, "empty.json")
	if err := os.WriteFile(emptyJSON, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty JSON: %v", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: emptyJSON, Path: "$.x"}); err != nil {
		t.Fatalf("unmerge empty JSON should no-op: %v", err)
	}
	nullJSON := filepath.Join(tmp, "null.json")
	if err := os.WriteFile(nullJSON, []byte("null"), 0o644); err != nil {
		t.Fatalf("write null JSON: %v", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: nullJSON, Path: "$.x"}); err != nil {
		t.Fatalf("unmerge nil JSON root should no-op: %v", err)
	}
	badJSON := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{bad"), 0o644); err != nil {
		t.Fatalf("write bad JSON: %v", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: badJSON, Path: "$.x"}); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("unmerge bad JSON err=%v; want parse", err)
	}
	if err := os.WriteFile(badJSON, []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatalf("write JSON: %v", err)
	}
	if err := unmergeJSONKey(MergedKeySelector{File: badJSON, Path: "bad"}); err == nil || !strings.Contains(err.Error(), "parse path") {
		t.Fatalf("unmerge bad JSON path err=%v; want parse path", err)
	}

	missingTOML := filepath.Join(tmp, "missing.toml")
	if err := unmergeTOMLKey(MergedKeySelector{File: missingTOML, Path: "x"}); err != nil {
		t.Fatalf("unmerge missing TOML should no-op: %v", err)
	}
	emptyTOML := filepath.Join(tmp, "empty.toml")
	if err := os.WriteFile(emptyTOML, []byte("  \n"), 0o644); err != nil {
		t.Fatalf("write empty TOML: %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: emptyTOML, Path: "x"}); err != nil {
		t.Fatalf("unmerge empty TOML should no-op: %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: emptyTOML, Path: "$.bad"}); err != nil {
		t.Fatalf("empty TOML returns before parsing path: %v", err)
	}
	if err := os.WriteFile(emptyTOML, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatalf("write TOML: %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: emptyTOML, Path: "$.bad"}); err == nil || !strings.Contains(err.Error(), "parse path") {
		t.Fatalf("unmerge bad TOML path err=%v; want parse path", err)
	}
}

func TestTOMLAppendSuccessAndExistingAppendCleanupBranches(t *testing.T) {
	tmp := t.TempDir()
	tomlPath := filepath.Join(tmp, "config.toml")
	value := map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "true"}}}
	if err := applyMergedKey(adapter.MergedKeyWrite{File: tomlPath, Path: "hooks.PreToolUse", Value: value, Op: adapter.MergedKeyAppend}); err != nil {
		t.Fatalf("apply TOML append: %v", err)
	}
	selector, err := selectorFor(value)
	if err != nil {
		t.Fatalf("selectorFor: %v", err)
	}
	if err := unmergeTOMLKey(MergedKeySelector{File: tomlPath, Path: "hooks.PreToolUse", Op: adapter.MergedKeyAppend, Selector: selector}); err != nil {
		t.Fatalf("unmerge TOML append: %v", err)
	}

	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	rec := manifest.Record{ID: "host:hook:guard", Scope: "user"}
	if err := unmergeExistingAppendsForRecord(store, rec); err != nil {
		t.Fatalf("no existing record cleanup: %v", err)
	}
	if err := store.Upsert(manifest.Record{
		ID:    rec.ID,
		Scope: rec.Scope,
		MergedKeys: []manifest.MergedKey{
			{File: tomlPath, Path: "mcp_servers.github", Op: string(adapter.MergedKeySet)},
		},
	}); err != nil {
		t.Fatalf("Upsert set-only: %v", err)
	}
	if err := unmergeExistingAppendsForRecord(store, rec); err != nil {
		t.Fatalf("set-only cleanup should no-op: %v", err)
	}
	if err := store.Upsert(manifest.Record{
		ID:    rec.ID,
		Scope: rec.Scope,
		MergedKeys: []manifest.MergedKey{
			{File: filepath.Join(tmp, "bad.ext"), Path: "hooks.PreToolUse", Op: string(adapter.MergedKeyAppend), Selector: "sha256:nope"},
		},
	}); err != nil {
		t.Fatalf("Upsert bad append: %v", err)
	}
	if err := unmergeExistingAppendsForRecord(store, rec); err == nil || !strings.Contains(err.Error(), "un-merge existing") {
		t.Fatalf("bad append cleanup err=%v; want un-merge existing", err)
	}

	badStorePath := filepath.Join(tmp, "bad-installs.yaml")
	if err := os.WriteFile(badStorePath, []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	if err := unmergeExistingAppendsForRecord(manifest.NewStore(badStorePath), rec); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("bad manifest cleanup err=%v; want load manifest", err)
	}
}

func TestPreflightMergedKeyCollisionErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	badStore := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badStore, []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	if _, err := preflightMergedKeyCollisions(manifest.NewStore(badStore), manifest.Record{ID: "x"}, nil); err == nil {
		t.Fatal("preflight should return manifest load error")
	}

	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	rec := manifest.Record{ID: "host:mcp-server:github", Scope: "user"}
	if err := store.Upsert(rec); err != nil {
		t.Fatalf("Upsert existing rec: %v", err)
	}
	jsonPath := filepath.Join(tmp, "settings.json")
	if err := os.WriteFile(jsonPath, []byte(`{"mcpServers":{"github":{"command":"manual"}}}`), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	collisions, err := preflightMergedKeyCollisions(store, rec, []adapter.MergedKeyWrite{{File: jsonPath, Path: "$.mcpServers.github", Value: "x"}})
	if err != nil || len(collisions) != 0 {
		t.Fatalf("same-identity preflight collisions=%v err=%v; want none", collisions, err)
	}

	otherStore := manifest.NewStore(filepath.Join(tmp, "other.yaml"))
	if _, err := preflightMergedKeyCollisions(otherStore, rec, []adapter.MergedKeyWrite{{File: filepath.Join(tmp, "x.ext"), Path: "$.x", Value: "y"}}); err == nil || !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("preflight unsupported format err=%v; want unsupported", err)
	}
	badJSON := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{bad"), 0o644); err != nil {
		t.Fatalf("write bad json: %v", err)
	}
	if _, err := preflightMergedKeyCollisions(otherStore, rec, []adapter.MergedKeyWrite{{File: badJSON, Path: "$.x", Value: "y"}}); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("preflight parse err=%v; want parse", err)
	}
	if err := os.WriteFile(badJSON, []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	if _, err := preflightMergedKeyCollisions(otherStore, rec, []adapter.MergedKeyWrite{{File: badJSON, Path: "bad", Value: "y"}}); err == nil || !strings.Contains(err.Error(), "parse path") {
		t.Fatalf("preflight bad path err=%v; want parse path", err)
	}
	if _, err := preflightMergedKeyCollisions(otherStore, rec, []adapter.MergedKeyWrite{{File: badJSON, Path: "$.hooks.PreToolUse", Value: make(chan int), Op: adapter.MergedKeyAppend}}); err == nil || !strings.Contains(err.Error(), "hash value") {
		t.Fatalf("preflight hash value err=%v; want hash value", err)
	}
}
