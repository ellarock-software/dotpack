package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/ellarock/dotpack/internal/adapter"
)

// TestApplyTOMLMergedKey_OpAppend_FreshFile pins the Op=Append wiring
// landed by the codex hook slice. Fresh file (no existing config.toml),
// one append into hooks.PreToolUse, asserts the canonical
// [[hooks.PreToolUse]] shape lands on disk. Replaced the stub-pin
// "not yet implemented" guard once the arm wired.
func TestApplyTOMLMergedKey_OpAppend_FreshFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	mk := adapter.MergedKeyWrite{
		File: path,
		Path: "hooks.PreToolUse",
		Value: map[string]any{
			"matcher": "Bash",
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": "/usr/local/bin/guard.sh",
			}},
		},
		Op: adapter.MergedKeyAppend,
	}
	if err := applyTOMLMergedKey(mk); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "[[hooks.PreToolUse]]") {
		t.Errorf("expected canonical array-of-tables shape; got:\n%s", raw)
	}
	if !strings.Contains(string(raw), "matcher = 'Bash'") {
		t.Errorf("expected matcher field on disk; got:\n%s", raw)
	}
}

// TestApplyTOMLMergedKey_OpAppend_AppendsIntoExisting pins that a
// second Op=Append into a pre-populated hooks.<Event> array preserves
// the first element AND appends the second — the same coexistence
// invariant that drove the JSON-side TestInstall_Hook_OrderOfInstalls
// pinning.
func TestApplyTOMLMergedKey_OpAppend_AppendsIntoExisting(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	first := adapter.MergedKeyWrite{
		File:  path,
		Path:  "hooks.PreToolUse",
		Value: map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "/usr/local/bin/a.sh"}}},
		Op:    adapter.MergedKeyAppend,
	}
	if err := applyTOMLMergedKey(first); err != nil {
		t.Fatalf("apply first: %v", err)
	}
	second := adapter.MergedKeyWrite{
		File:  path,
		Path:  "hooks.PreToolUse",
		Value: map[string]any{"matcher": "Edit", "hooks": []any{map[string]any{"type": "command", "command": "/usr/local/bin/b.sh"}}},
		Op:    adapter.MergedKeyAppend,
	}
	if err := applyTOMLMergedKey(second); err != nil {
		t.Fatalf("apply second: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "[[hooks.PreToolUse]]") != 2 {
		t.Errorf("expected two [[hooks.PreToolUse]] tables; got:\n%s", raw)
	}
}

// TestUnmergeTOMLKey_OpAppend_MissingSelectorErrors pins the manifest-
// shape invariant — Op=Append un-merge without a Selector indicates a
// buggy install that wrote a manifest entry without the content-hash
// identity, and the un-merge cannot recover. Better to surface than to
// silently no-op (which would orphan the array element).
func TestUnmergeTOMLKey_OpAppend_MissingSelectorErrors(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(path, []byte("[hooks]\n[[hooks.PreToolUse]]\nmatcher = 'Bash'\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mk := MergedKeySelector{
		File:     path,
		Path:     "hooks.PreToolUse",
		Op:       adapter.MergedKeyAppend,
		Selector: "",
	}
	err := unmergeTOMLKey(mk)
	if err == nil {
		t.Fatal("expected missing-selector to error; got nil")
	}
	if !strings.Contains(err.Error(), "Selector") {
		t.Errorf("error must name the missing field; got %v", err)
	}
}

// TestParseTOMLPath_RejectsJSONStylePrefix pins the cross-format
// safety net. An adapter that mistakenly emits "$.mcp_servers.foo"
// into a TOML KindConfig must error at parse rather than producing a
// top-level $ table entry.
func TestParseTOMLPath_RejectsJSONStylePrefix(t *testing.T) {
	_, err := parseTOMLPath("$.mcp_servers.foo")
	if err == nil {
		t.Fatal("expected parseTOMLPath to reject $-prefixed path; got nil")
	}
	if !strings.Contains(err.Error(), "must NOT have $ prefix") {
		t.Errorf("error must name the spurious prefix; got %v", err)
	}
}

// TestParseTOMLPath_EmptyAndEmptySegmentsRejected pins parser strictness
// matching parseJSONPath's contract.
func TestParseTOMLPath_EmptyAndEmptySegmentsRejected(t *testing.T) {
	for _, tc := range []string{"", "foo..bar", ".foo", "foo."} {
		if _, err := parseTOMLPath(tc); err == nil {
			t.Errorf("parseTOMLPath(%q): expected error; got nil", tc)
		}
	}
}

// TestNormalizeForTOML_IntegralFloatCoercion pins the integer-vs-float
// coercion at the merge-boundary value. The JSON-decoded `30` arrives
// as float64(30); after normalizeForTOML it must be int64(30) so
// toml.Marshal emits `30` not `30.0`.
func TestNormalizeForTOML_IntegralFloatCoercion(t *testing.T) {
	v, err := normalizeForTOML(float64(30))
	if err != nil {
		t.Fatalf("normalize integral float: %v", err)
	}
	got, ok := v.(int64)
	if !ok || got != 30 {
		t.Errorf("integral float64(30) → want int64(30); got %T = %v", v, v)
	}

	// Non-integral float64 must stay float64.
	v2, err := normalizeForTOML(float64(30.5))
	if err != nil {
		t.Fatalf("normalize non-integral float: %v", err)
	}
	if _, ok := v2.(float64); !ok {
		t.Errorf("non-integral float64(30.5) must stay float64; got %T", v2)
	}
}

// TestNormalizeForTOML_NilMapEntryDropped pins the nil-as-map-value
// drop posture (matches go-toml/v2's silent drop, just made explicit
// in dotpack's walker).
func TestNormalizeForTOML_NilMapEntryDropped(t *testing.T) {
	in := map[string]any{"keep": "yes", "drop": nil}
	v, err := normalizeForTOML(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	out := v.(map[string]any)
	if _, present := out["drop"]; present {
		t.Errorf("nil map value should be dropped; got %v", out)
	}
	if out["keep"] != "yes" {
		t.Errorf("non-nil entries should survive; got %v", out)
	}
}

// TestNormalizeForTOML_NilSliceElementErrors pins the structured
// error on slice-nil (go-toml/v2 errors anyway; we surface a clearer
// message naming the index).
func TestNormalizeForTOML_NilSliceElementErrors(t *testing.T) {
	in := []any{"ok", nil, "also-ok"}
	_, err := normalizeForTOML(in)
	if err == nil {
		t.Fatal("nil slice element should error; got nil")
	}
	if !strings.Contains(err.Error(), "[1]") {
		t.Errorf("error must name the offending index; got %v", err)
	}
}

// TestWriteTOML_DoesNotCoerceUserAuthoredFloats pins the hostile-review
// #1 fix at the unit level — writeTOML must NOT walk the root through
// normalizeForTOML. A root containing float64(1.0) must emit `1.0`,
// not `1`. The corruption-via-coercion path now lives ONLY inside
// applyTOMLMergedKey, scoped to mk.Value.
func TestWriteTOML_DoesNotCoerceUserAuthoredFloats(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	// A root that came from toml.Unmarshal — version is float64(1.0).
	root := map[string]any{
		"version": float64(1.0),
		"name":    "user-authored",
	}
	if err := writeTOML(path, root); err != nil {
		t.Fatalf("writeTOML: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "version = 1.0") {
		t.Errorf("writeTOML must preserve float64(1.0) as `1.0`, not coerce; got:\n%s", raw)
	}

	// Symmetric: re-decode, type is still float64.
	var rt map[string]any
	if err := toml.Unmarshal(raw, &rt); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if _, ok := rt["version"].(float64); !ok {
		t.Errorf("round-trip type drift; got %T", rt["version"])
	}
}
