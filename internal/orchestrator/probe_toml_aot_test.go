package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestTOMLArrayOfTablesRoundTripFidelity_HashStable is the regression
// guard for the format-boundary invariant the codex-hook slice depends
// on. Originated as a one-shot probe before Op=Append on TOML was
// wired; promoted to a permanent regression test once the wiring
// landed because it pins the load-bearing un-merge-by-content-hash
// invariant at the format-boundary layer (the integration tests in
// install_hook_codex_test.go pin it end-to-end). Documents:
//
//  1. The on-disk shape go-toml/v2 produces when Marshalling a
//     map[string]any with `hooks: map{PreToolUse: []any{map{...}}}`
//     — does it emit `[[hooks.PreToolUse]]` (array-of-tables, the
//     canonical hand-authored shape per schema/hook.yaml) or
//     `hooks.PreToolUse = [{...}]` (inline-array-of-inline-tables)?
//  2. The round-trip type fidelity: after Marshal → Unmarshal, does the
//     element come back as map[string]any with the same keys + values?
//  3. selectorFor hash stability across the emit→TOML→re-parse cycle:
//     hash(json.Marshal(emit-time value)) MUST equal
//     hash(json.Marshal(toml.Unmarshal(emit-bytes)[<path>])) — that's
//     the load-bearing invariant for un-merge-by-content-hash.
//
// Why this matters: claudecode hook's selectorFor stays stable across
// JSON→JSON round-trip (proven in v17). The codex hook slice adds JSON
// emit → TOML write → TOML re-parse → JSON-marshal-for-hash. If
// pelletier/go-toml/v2's round-trip mutates types (e.g., demotes a
// map[string]string `env` block to a typed inline-table representation
// that json.Marshal renders differently), un-merge would never find
// the matching element and the manifest would orphan.
//
// Per-format companion to TestWriteTOML_DoesNotCoerceUserAuthoredFloats
// in mergedkeys_toml_test.go. A go-toml/v2 upgrade that breaks any of
// the four shapes here would surface in this single failure rather
// than in a downstream un-merge-orphan ghost.
func TestTOMLArrayOfTablesRoundTripFidelity_HashStable(t *testing.T) {
	// Mimic emitHookCodex's planned output: one binding fragment under
	// hooks.PreToolUse with universal-core fields + a hook-spec leaf.
	bindingValue := map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "/usr/local/bin/bash-guard.sh",
			},
		},
	}

	// Phase 1: simulate apply-time append into a fresh-file root.
	root := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{bindingValue},
		},
	}

	out, err := toml.Marshal(root)
	if err != nil {
		t.Fatalf("toml.Marshal root: %v", err)
	}
	emitted := string(out)
	t.Logf("on-disk TOML shape:\n%s", emitted)

	// Phase 2: assert the canonical hand-authored shape. The schema's
	// in-the-wild-non-spec note flags `[[hooks]] event = '...'` as a
	// shape we should accept on import but NOT emit — the canonical
	// form is array-of-tables nested under the event name.
	if !strings.Contains(emitted, "[[hooks.PreToolUse]]") {
		t.Errorf("expected canonical [[hooks.PreToolUse]] shape; got:\n%s", emitted)
	}

	// Phase 3: round-trip type fidelity. After Marshal → Unmarshal,
	// the binding element must be map[string]any with the same keys
	// and string values.
	var rt map[string]any
	if err := toml.Unmarshal(out, &rt); err != nil {
		t.Fatalf("toml.Unmarshal: %v", err)
	}
	hooks, ok := rt["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key is %T; want map[string]any", rt["hooks"])
	}
	preToolUse, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatalf("hooks.PreToolUse is %T; want []any", hooks["PreToolUse"])
	}
	if len(preToolUse) != 1 {
		t.Fatalf("expected 1 element; got %d", len(preToolUse))
	}
	rtBinding, ok := preToolUse[0].(map[string]any)
	if !ok {
		t.Fatalf("binding is %T; want map[string]any", preToolUse[0])
	}
	if rtBinding["matcher"] != "Bash" {
		t.Errorf("matcher round-trip: got %v; want Bash", rtBinding["matcher"])
	}

	// Phase 4: selectorFor hash stability — the load-bearing invariant.
	// Hash the EMIT-TIME value (what install computes) and the
	// ROUND-TRIPPED value (what un-merge re-derives from the file).
	// They must match for un-merge-by-content-hash to function.
	hashEmit, err := hashValue(bindingValue)
	if err != nil {
		t.Fatalf("hash emit: %v", err)
	}
	hashRoundTrip, err := hashValue(rtBinding)
	if err != nil {
		t.Fatalf("hash round-trip: %v", err)
	}
	if hashEmit != hashRoundTrip {
		// Diagnostic: log the JSON byte forms so a future debugger
		// sees what diverged (key order? whitespace? type drift?).
		emitJSON, _ := json.Marshal(bindingValue)
		rtJSON, _ := json.Marshal(rtBinding)
		t.Errorf("selectorFor hash DIVERGES across JSON→TOML→JSON round-trip — un-merge would orphan.\n  emit:       %s -> %s\n  round-trip: %s -> %s",
			emitJSON, hashEmit, rtJSON, hashRoundTrip)
	}

	// Phase 5: env-bearing variant (env-hook.hook.json shape). env is
	// map[string]string at emit time; toml round-trip lands it as
	// map[string]any with string values — json.Marshal of either form
	// produces identical bytes per emitHook's docstring (hostile-review
	// #4 from v16). Pin it here so a future go-toml/v2 upgrade that
	// breaks the assumption surfaces immediately.
	envBindingValue := map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "/usr/local/bin/env-aware.sh",
				"env": map[string]string{
					"SECRET_TOKEN": "shh",
					"TIER":         "prod",
				},
			},
		},
	}
	envRoot := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{envBindingValue},
		},
	}
	envOut, err := toml.Marshal(envRoot)
	if err != nil {
		t.Fatalf("toml.Marshal env root: %v", err)
	}
	t.Logf("env-bearing TOML shape:\n%s", envOut)

	var envRT map[string]any
	if err := toml.Unmarshal(envOut, &envRT); err != nil {
		t.Fatalf("toml.Unmarshal env: %v", err)
	}
	envRTBinding := envRT["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	envHashEmit, _ := hashValue(envBindingValue)
	envHashRT, _ := hashValue(envRTBinding)
	if envHashEmit != envHashRT {
		emitJSON, _ := json.Marshal(envBindingValue)
		rtJSON, _ := json.Marshal(envRTBinding)
		t.Errorf("env-bearing selectorFor hash DIVERGES across round-trip.\n  emit:       %s -> %s\n  round-trip: %s -> %s",
			emitJSON, envHashEmit, rtJSON, envHashRT)
	}

	// Phase 5b: timeout-bearing variant. spec.Timeout is Go `int`, emit
	// passes it through as int, json.Marshal renders `"timeout":30`,
	// TOML round-trip lands it as int64(30), json.Marshal of THAT also
	// renders `30`. Hash stable across the round-trip. Pinned here
	// because (a) the universal core's only non-string-or-map field is
	// timeout, (b) without this case the probe doesn't actually exercise
	// the integer round-trip, and (c) selectorFor is computed at install
	// time from un-normalized mk.Value per buildRecord — for the int
	// case normalize is a no-op anyway, but a future hook extension
	// carrying float64 would break the invariant (see normalize-vs-hash
	// note on applyTOMLMergedKey's Op=Append arm).
	timeoutBindingValue := map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "/usr/local/bin/slow.sh",
				"timeout": 30,
			},
		},
	}
	timeoutRoot := map[string]any{
		"hooks": map[string]any{"PreToolUse": []any{timeoutBindingValue}},
	}
	timeoutOut, err := toml.Marshal(timeoutRoot)
	if err != nil {
		t.Fatalf("toml.Marshal timeout root: %v", err)
	}
	t.Logf("timeout-bearing TOML shape:\n%s", timeoutOut)
	var timeoutRT map[string]any
	if err := toml.Unmarshal(timeoutOut, &timeoutRT); err != nil {
		t.Fatalf("toml.Unmarshal timeout: %v", err)
	}
	timeoutRTBinding := timeoutRT["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	timeoutHashEmit, _ := hashValue(timeoutBindingValue)
	timeoutHashRT, _ := hashValue(timeoutRTBinding)
	if timeoutHashEmit != timeoutHashRT {
		emitJSON, _ := json.Marshal(timeoutBindingValue)
		rtJSON, _ := json.Marshal(timeoutRTBinding)
		t.Errorf("timeout-bearing selectorFor hash DIVERGES across round-trip.\n  emit:       %s -> %s\n  round-trip: %s -> %s",
			emitJSON, timeoutHashEmit, rtJSON, timeoutHashRT)
	}

	// Phase 6: append-into-existing — confirm a second append into a
	// pre-populated array produces the expected on-disk shape (two
	// [[hooks.PreToolUse]] tables, not a degenerate single table or a
	// reverted inline array).
	root["hooks"].(map[string]any)["PreToolUse"] = append(
		root["hooks"].(map[string]any)["PreToolUse"].([]any),
		map[string]any{
			"matcher": "Edit|Write",
			"hooks":   []any{map[string]any{"type": "command", "command": "/usr/local/bin/pre-write.sh"}},
		},
	)
	out2, err := toml.Marshal(root)
	if err != nil {
		t.Fatalf("toml.Marshal append: %v", err)
	}
	t.Logf("after-append TOML shape:\n%s", out2)
	if strings.Count(string(out2), "[[hooks.PreToolUse]]") != 2 {
		t.Errorf("expected two [[hooks.PreToolUse]] tables after append; got:\n%s", out2)
	}
}

// hashValue mirrors selectorFor's algorithm verbatim. Kept local so
// the probe doesn't depend on the production helper changing shape;
// the assertion is "this byte sequence + this hash function reproduce
// stable identity across a JSON→TOML→JSON round-trip."
func hashValue(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}
