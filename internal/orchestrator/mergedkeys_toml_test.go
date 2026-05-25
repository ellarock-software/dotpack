package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/ellarock/dotpack/internal/adapter"
)

// TestApplyTOMLMergedKey_OpAppend_NotYetImplemented pins the
// hostile-review #4 defensive guard. Op=Append on TOML is unreachable
// from current adapters (codex mcp-server is Op=Set; no codex hook emit
// yet), but the structured error message must remain stable so the
// future codex-hook slice's tracer-bullet RED fails with a clear
// pointer rather than silently no-oping.
func TestApplyTOMLMergedKey_OpAppend_NotYetImplemented(t *testing.T) {
	tmp := t.TempDir()
	mk := adapter.MergedKeyWrite{
		File:  filepath.Join(tmp, "config.toml"),
		Path:  "hooks.PreToolUse",
		Value: map[string]any{"matcher": "*"},
		Op:    adapter.MergedKeyAppend,
	}
	err := applyTOMLMergedKey(mk)
	if err == nil {
		t.Fatal("expected Op=Append on TOML to error; got nil")
	}
	if !strings.Contains(err.Error(), "Op=Append on TOML not yet implemented") {
		t.Errorf("error must name the unwired arm; got %v", err)
	}
	if !strings.Contains(err.Error(), "codex hook slice") {
		t.Errorf("error must point at the slice that wires it; got %v", err)
	}
	// File must not be created when the operation errors.
	if _, statErr := os.Stat(mk.File); !os.IsNotExist(statErr) {
		t.Errorf("file must not be created on errored apply; stat: %v", statErr)
	}
}

// TestUnmergeTOMLKey_OpAppend_NotYetImplemented mirrors the apply guard
// for the un-merge path.
func TestUnmergeTOMLKey_OpAppend_NotYetImplemented(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(path, []byte("[hooks]\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mk := MergedKeySelector{
		File:     path,
		Path:     "hooks.PreToolUse",
		Op:       adapter.MergedKeyAppend,
		Selector: "sha256:0",
	}
	err := unmergeTOMLKey(mk)
	if err == nil {
		t.Fatal("expected Op=Append on TOML un-merge to error; got nil")
	}
	if !strings.Contains(err.Error(), "Op=Append on TOML not yet implemented") {
		t.Errorf("error must name the unwired arm; got %v", err)
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
