package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// codexHookInstallHelper installs the named hook fixture onto codex at user
// scope and returns the resolved config.toml path. Single-call helper
// for the scenario tests so env setup + invocation lives in one place.
func codexHookInstallHelper(t *testing.T, codexHome, dotpackHome, srcName string) string {
	t.Helper()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", codexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	src := filepath.Join("..", "resource", "testdata", "hooks", srcName)
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install %s: %v\n%s", srcName, err, out.String())
	}
	return filepath.Join(codexHome, "config.toml")
}

// codexHookUninstall is the codex-side mirror of hookUninstall.
func codexHookUninstall(t *testing.T, id string) {
	t.Helper()
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"uninstall", id})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("uninstall %s: %v", id, err)
	}
}

// readCodexHooks parses config.toml into the event → []binding shape
// the claudecode-side readHooks already produces, so assertions can
// match the JSON-side test style. The map value types after
// toml.Unmarshal are map[string]any / []any — same shape json.Unmarshal
// produces, so the assertion code in each test reads identical to its
// JSON sibling.
func readCodexHooks(t *testing.T, path string) map[string][]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, raw)
	}
	hooks, _ := root["hooks"].(map[string]any)
	out := map[string][]map[string]any{}
	for evt, arr := range hooks {
		a, _ := arr.([]any)
		var bindings []map[string]any
		for _, el := range a {
			if b, ok := el.(map[string]any); ok {
				bindings = append(bindings, b)
			}
		}
		out[evt] = bindings
	}
	return out
}

// TestInstall_HookOnCodex_UserScope_FreshFile is the tracer-bullet for
// the codex hook slice per ADR-0016 §5–§7+§9. Smallest meaningful
// vertical slice: one event × one binding × one hook-spec on codex,
// TOML format, user scope, fresh config.toml (no existing file).
//
// Forces the new architectural elements this slice landed:
//   - orchestrator.applyTOMLMergedKey + unmergeTOMLKey Op=Append arms
//     wired (previously stub-pinned)
//   - codex.emitHookCodex + KindHook entry in configfragPolicy()
//   - schema/hook.yaml's codex source_locations file
//     (~/.codex/config.toml) actually written
//   - selectorFor hash stability across JSON-emit → TOML-write → TOML-
//     re-parse → JSON-hash (the load-bearing invariant for un-merge-by-
//     content-hash; proven by orchestrator/probe_toml_aot_test.go)
//
// What this test pins:
//
//  1. `dotpack install <src> --agent codex --kind hook --scope user`
//     writes ~/.codex/config.toml with [[hooks.PreToolUse]] containing
//     matcher + nested [[hooks.PreToolUse.hooks]] hook-spec leaf.
//  2. Manifest record has ID codex:hook:bash-guard, Kind hook, one
//     merged_keys entry with file=config.toml, path="hooks.PreToolUse"
//     (TOML-dotted, no `$.` prefix), op="append", selector=sha256:...
//  3. Uninstall removes the binding (matched by Selector hash, not
//     numeric index) AND removes the manifest record. The config.toml
//     file itself stays; whether the empty PreToolUse array survives
//     is a host-config-retention decision (this test does NOT pin
//     removal — only that the install's binding is gone).
func TestInstall_HookOnCodex_UserScope_FreshFile(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	configPath := codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	asStr := string(raw)
	// Canonical array-of-tables shape on disk — what schema/hook.yaml
	// names the codex hook spec. Hand-author-friendly.
	if !strings.Contains(asStr, "[[hooks.PreToolUse]]") {
		t.Errorf("expected [[hooks.PreToolUse]] in output; got:\n%s", asStr)
	}
	if !strings.Contains(asStr, "matcher = 'Bash'") {
		t.Errorf("expected matcher = 'Bash' on disk; got:\n%s", asStr)
	}
	if !strings.Contains(asStr, "[[hooks.PreToolUse.hooks]]") {
		t.Errorf("expected nested hook-spec array-of-tables; got:\n%s", asStr)
	}
	if !strings.Contains(asStr, "command = '/usr/local/bin/bash-guard.sh'") {
		t.Errorf("expected command leaf on disk; got:\n%s", asStr)
	}

	// Structural re-parse for robust assertion (string-match assertions
	// guard against typos in the on-disk format; the parsed assertion
	// guards against the schema shape).
	hooks := readCodexHooks(t, configPath)
	if len(hooks["PreToolUse"]) != 1 {
		t.Fatalf("expected one PreToolUse binding; got %d", len(hooks["PreToolUse"]))
	}
	binding := hooks["PreToolUse"][0]
	if binding["matcher"] != "Bash" {
		t.Errorf("matcher = %v; want Bash", binding["matcher"])
	}
	specs, _ := binding["hooks"].([]any)
	if len(specs) != 1 {
		t.Fatalf("expected one hook-spec; got %d", len(specs))
	}
	spec, _ := specs[0].(map[string]any)
	if spec["type"] != "command" || spec["command"] != "/usr/local/bin/bash-guard.sh" {
		t.Errorf("hook-spec leaf wrong: %v", spec)
	}

	// Manifest record: codex:hook:bash-guard with Op=append + non-
	// empty sha256 Selector.
	type mkPersisted struct {
		File     string `yaml:"file"`
		Path     string `yaml:"path"`
		Op       string `yaml:"op,omitempty"`
		Selector string `yaml:"selector,omitempty"`
	}
	var manifestRaw struct {
		Installs []struct {
			ID         string        `yaml:"id"`
			Kind       string        `yaml:"kind"`
			Agent      string        `yaml:"agent"`
			Files      []string      `yaml:"files,omitempty"`
			MergedKeys []mkPersisted `yaml:"merged_keys,omitempty"`
		} `yaml:"installs"`
	}
	mr, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := yaml.Unmarshal(mr, &manifestRaw); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, mr)
	}
	if len(manifestRaw.Installs) != 1 {
		t.Fatalf("expected 1 install record; got %d (%s)", len(manifestRaw.Installs), mr)
	}
	rec := manifestRaw.Installs[0]
	if rec.ID != "codex:hook:bash-guard" {
		t.Errorf("record ID = %q; want codex:hook:bash-guard", rec.ID)
	}
	if rec.Kind != "hook" {
		t.Errorf("record Kind = %q; want hook", rec.Kind)
	}
	if rec.Agent != "codex" {
		t.Errorf("record Agent = %q; want codex", rec.Agent)
	}
	if len(rec.Files) != 0 {
		t.Errorf("config-fragment install must not claim files; got %v", rec.Files)
	}
	if len(rec.MergedKeys) != 1 {
		t.Fatalf("expected 1 merged_keys entry; got %d (%v)", len(rec.MergedKeys), rec.MergedKeys)
	}
	mk := rec.MergedKeys[0]
	if mk.File != configPath {
		t.Errorf("merged_keys[0].file = %q; want %q", mk.File, configPath)
	}
	if mk.Path != "hooks.PreToolUse" {
		t.Errorf("merged_keys[0].path = %q; want hooks.PreToolUse (TOML-dotted, no $ prefix)", mk.Path)
	}
	if mk.Op != "append" {
		t.Errorf("merged_keys[0].op = %q; want append", mk.Op)
	}
	selectorRE := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if !selectorRE.MatchString(mk.Selector) {
		t.Errorf("merged_keys[0].selector = %q; want sha256:<64-hex>", mk.Selector)
	}

	// Uninstall round-trip — the load-bearing TOML Op=Append un-merge.
	codexHookUninstall(t, "codex:hook:bash-guard")

	if raw2, err := os.ReadFile(configPath); err == nil {
		var parsed2 map[string]any
		if jerr := toml.Unmarshal(raw2, &parsed2); jerr == nil {
			if hooks2, ok := parsed2["hooks"].(map[string]any); ok {
				if arr, ok := hooks2["PreToolUse"].([]any); ok && len(arr) > 0 {
					for _, el := range arr {
						if b, ok := el.(map[string]any); ok && b["matcher"] == "Bash" {
							if specs, ok := b["hooks"].([]any); ok && len(specs) == 1 {
								if s, ok := specs[0].(map[string]any); ok && s["command"] == "/usr/local/bin/bash-guard.sh" {
									t.Errorf("expected hooks.PreToolUse to no longer contain the bash-guard binding after uninstall; got:\n%s", raw2)
								}
							}
						}
					}
				}
			}
		}
	}

	mr2, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatalf("read manifest after uninstall: %v", err)
	}
	manifestRaw.Installs = nil
	if err := yaml.Unmarshal(mr2, &manifestRaw); err != nil {
		t.Fatalf("parse manifest after uninstall: %v", err)
	}
	if len(manifestRaw.Installs) != 0 {
		t.Errorf("expected 0 install records after uninstall; got %d", len(manifestRaw.Installs))
	}
}

// TestInstall_HookOnCodex_SiblingKeyPreservation pins the "config.toml
// is user-owned, not dotpack-owned" contract for codex. A pre-existing
// config.toml with user-authored top-level `profile`, an unrelated
// [mcp_servers.linear] table, AND user-authored [[hooks.SessionStart]]
// + [[hooks.PreToolUse]] entries must survive the install + uninstall
// round-trip with their non-dotpack bytes intact.
//
// Mirror of TestInstall_Hook_SiblingKeyPreservation_AcrossInstall
// Uninstall on claudecode — same invariant, different format.
func TestInstall_HookOnCodex_SiblingKeyPreservation(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()

	configPath := filepath.Join(codexHome, "config.toml")
	preBytes := []byte(`profile = "user-default"

[mcp_servers.linear]
command = "linear-bin"
args = ["--token", "${LINEAR_TOKEN}"]

[[hooks.SessionStart]]
[[hooks.SessionStart.hooks]]
type = "command"
command = "/usr/local/bin/welcome.sh"

[[hooks.PreToolUse]]
matcher = "Read"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/local/bin/read-guard.sh"
`)
	if err := os.WriteFile(configPath, preBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	got := codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")
	if got != configPath {
		t.Fatalf("unexpected config path: %s", got)
	}

	raw, _ := os.ReadFile(configPath)
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse after install: %v", err)
	}
	if parsed["profile"] != "user-default" {
		t.Errorf("top-level profile mutated/missing: %v", parsed["profile"])
	}
	servers, _ := parsed["mcp_servers"].(map[string]any)
	if linear, ok := servers["linear"].(map[string]any); !ok {
		t.Errorf("mcp_servers.linear disappeared after install: %v", servers)
	} else if linear["command"] != "linear-bin" {
		t.Errorf("mcp_servers.linear.command mutated: %v", linear)
	}

	hooks := readCodexHooks(t, configPath)
	if len(hooks["SessionStart"]) != 1 || hooks["SessionStart"][0]["hooks"].([]any)[0].(map[string]any)["command"] != "/usr/local/bin/welcome.sh" {
		t.Errorf("SessionStart binding mutated: %v", hooks["SessionStart"])
	}
	if len(hooks["PreToolUse"]) != 2 {
		t.Fatalf("expected PreToolUse to contain user-read-guard + dotpack-bash-guard; got %d: %v", len(hooks["PreToolUse"]), hooks["PreToolUse"])
	}

	// Uninstall: dotpack's bash-guard goes; user's read-guard stays;
	// profile + mcp_servers + SessionStart untouched.
	codexHookUninstall(t, "codex:hook:bash-guard")
	hooksAfter := readCodexHooks(t, configPath)
	if len(hooksAfter["PreToolUse"]) != 1 {
		t.Fatalf("expected user's read-guard to survive; got %d: %v", len(hooksAfter["PreToolUse"]), hooksAfter["PreToolUse"])
	}
	if hooksAfter["PreToolUse"][0]["matcher"] != "Read" {
		t.Errorf("surviving binding is not user's read-guard: %v", hooksAfter["PreToolUse"][0])
	}
	if len(hooksAfter["SessionStart"]) != 1 {
		t.Errorf("SessionStart binding lost during uninstall: %v", hooksAfter["SessionStart"])
	}
	finalRaw, _ := os.ReadFile(configPath)
	var final map[string]any
	if err := toml.Unmarshal(finalRaw, &final); err != nil {
		t.Fatalf("parse after uninstall: %v", err)
	}
	if final["profile"] != "user-default" {
		t.Errorf("profile disappeared after uninstall: %s", finalRaw)
	}
	if servers, ok := final["mcp_servers"].(map[string]any); !ok || servers["linear"] == nil {
		t.Errorf("mcp_servers.linear disappeared after uninstall: %s", finalRaw)
	}
}

// TestInstall_HookOnCodex_MultiBinding_RoundTrip pins ADR-0016 §9's
// "Multiple keys per hook install (one per binding leaf path)" for
// TOML. One resource installs 3 bindings across 2 events (PreToolUse ×
// 2 + PostToolUse × 1). Manifest must record 3 MergedKey entries (each
// with its own Selector); install writes all 3 to config.toml;
// uninstall removes all 3 in one go.
func TestInstall_HookOnCodex_MultiBinding_RoundTrip(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	configPath := codexHookInstallHelper(t, codexHome, dotpackHome, "multi-binding.hook.json")

	hooks := readCodexHooks(t, configPath)
	if len(hooks["PreToolUse"]) != 2 {
		t.Fatalf("expected PreToolUse to have 2 bindings; got %d", len(hooks["PreToolUse"]))
	}
	if len(hooks["PostToolUse"]) != 1 {
		t.Fatalf("expected PostToolUse to have 1 binding; got %d", len(hooks["PostToolUse"]))
	}
	matchers := []string{
		hooks["PreToolUse"][0]["matcher"].(string),
		hooks["PreToolUse"][1]["matcher"].(string),
	}
	if matchers[0] != "Bash" || matchers[1] != "Edit|Write" {
		t.Errorf("PreToolUse matcher order = %v; want [Bash, Edit|Write] (emit preserves source order)", matchers)
	}

	// Manifest must record 3 separate MergedKey entries with pairwise-
	// distinct Selectors.
	mr, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	type mkPersisted struct {
		File     string `yaml:"file"`
		Path     string `yaml:"path"`
		Op       string `yaml:"op,omitempty"`
		Selector string `yaml:"selector,omitempty"`
	}
	var m struct {
		Installs []struct {
			MergedKeys []mkPersisted `yaml:"merged_keys"`
		} `yaml:"installs"`
	}
	if err := yaml.Unmarshal(mr, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Installs) != 1 {
		t.Fatalf("expected 1 record; got %d", len(m.Installs))
	}
	if len(m.Installs[0].MergedKeys) != 3 {
		t.Fatalf("expected 3 merged_keys (2 PreToolUse + 1 PostToolUse); got %d: %v", len(m.Installs[0].MergedKeys), m.Installs[0].MergedKeys)
	}
	seen := map[string]struct{}{}
	for i, mk := range m.Installs[0].MergedKeys {
		if mk.Op != "append" {
			t.Errorf("merged_keys[%d].op = %q; want append", i, mk.Op)
		}
		if mk.Selector == "" {
			t.Errorf("merged_keys[%d].selector is empty", i)
		}
		if _, dup := seen[mk.Selector]; dup {
			t.Errorf("merged_keys[%d].selector %q duplicates an earlier entry", i, mk.Selector)
		}
		seen[mk.Selector] = struct{}{}
	}

	// Uninstall: all 3 bindings gone, manifest record removed.
	codexHookUninstall(t, "codex:hook:multi-binding")
	after := readCodexHooks(t, configPath)
	if len(after["PreToolUse"]) != 0 {
		t.Errorf("expected PreToolUse empty after uninstall; got %d", len(after["PreToolUse"]))
	}
	if len(after["PostToolUse"]) != 0 {
		t.Errorf("expected PostToolUse empty after uninstall; got %d", len(after["PostToolUse"]))
	}
	mr2, _ := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if strings.Contains(string(mr2), "codex:hook:multi-binding") {
		t.Errorf("expected manifest record removed; got %s", mr2)
	}
}

// TestInstall_HookOnCodex_EnvField_HashStable pins the env-field round-
// trip on TOML. Emit serialises env as map[string]string; uninstall
// re-reads via toml.Unmarshal into map[string]any. Both json.Marshal
// paths must produce byte-identical bytes for selectorFor to match.
// Without this, env-bearing hook installs would survive install but
// fail to un-merge on TOML (drift-tolerant no-op masks the regression).
// Mirror of TestInstall_Hook_EnvField_HashStableInstallToUninstall on
// claudecode; codex-specific because TOML round-trip introduces a
// different deserialization path (toml.Unmarshal vs json.Unmarshal).
//
// Hostile-review #4 follow-on from slice v16, extended to TOML.
func TestInstall_HookOnCodex_EnvField_HashStable(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	configPath := codexHookInstallHelper(t, codexHome, dotpackHome, "env-hook.hook.json")

	hooks := readCodexHooks(t, configPath)
	if len(hooks["PreToolUse"]) != 1 {
		t.Fatalf("expected 1 binding; got %d", len(hooks["PreToolUse"]))
	}
	specs, _ := hooks["PreToolUse"][0]["hooks"].([]any)
	envMap, _ := specs[0].(map[string]any)["env"].(map[string]any)
	if envMap["SECRET_TOKEN"] != "shh" || envMap["TIER"] != "prod" {
		t.Errorf("env not preserved correctly: %v", envMap)
	}

	// Uninstall: if the JSON-emit hash didn't match the TOML-roundtrip
	// hash, the array element would survive (drift-tolerant no-op)
	// and this assertion would fail. The probe in
	// orchestrator/probe_toml_aot_test.go pins the invariant at the
	// hash level; this test pins it end-to-end.
	codexHookUninstall(t, "codex:hook:env-hook")
	after := readCodexHooks(t, configPath)
	if len(after["PreToolUse"]) != 0 {
		t.Errorf("expected env-bearing binding to be removed by uninstall (hash must match install side); got %v", after["PreToolUse"])
	}
}

// TestInstall_HookOnCodex_UserEdit_DriftTolerantUninstall pins
// "drift on uninstall is intentional" for codex hook: when the user
// has edited dotpack's installed binding, uninstall's content-hash
// scan no longer matches — the function no-ops on the array but still
// removes the manifest record.
func TestInstall_HookOnCodex_UserEdit_DriftTolerantUninstall(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	configPath := codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")

	// Mutate the dotpack-installed binding's command via a TOML-shape
	// rewrite. Re-emit the whole file with the new command so we don't
	// depend on go-toml/v2's in-place mutation.
	mutated := []byte(`[[hooks.PreToolUse]]
matcher = "Bash"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "/usr/local/bin/USER-EDITED.sh"
`)
	if err := os.WriteFile(configPath, mutated, 0o644); err != nil {
		t.Fatal(err)
	}

	codexHookUninstall(t, "codex:hook:bash-guard")

	after := readCodexHooks(t, configPath)
	if len(after["PreToolUse"]) != 1 {
		t.Fatalf("expected user-edited binding to survive uninstall; got %d", len(after["PreToolUse"]))
	}
	specs, _ := after["PreToolUse"][0]["hooks"].([]any)
	cmd, _ := specs[0].(map[string]any)["command"].(string)
	if cmd != "/usr/local/bin/USER-EDITED.sh" {
		t.Errorf("expected USER-EDITED command to survive; got %q", cmd)
	}
	mr, _ := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if strings.Contains(string(mr), "codex:hook:bash-guard") {
		t.Errorf("expected manifest record removed; got %s", mr)
	}
}

// TestInstall_HookOnCodex_ReInstall_ReplacesNotDuplicates pins the
// re-install contract for Op=Append on TOML: installing the same ID a
// second time (with different content) must REPLACE the old array
// element, not append alongside. unmergeExistingAppendsForID is the
// format-agnostic mechanism that fires on both JSON and TOML.
func TestInstall_HookOnCodex_ReInstall_ReplacesNotDuplicates(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	configPath := codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")

	// Re-install with edited source under the same filename-derived
	// name. Temp source file avoids touching the testdata fixture.
	tempSrc := filepath.Join(t.TempDir(), "bash-guard.hook.json")
	if err := os.WriteFile(tempSrc, []byte(`{
		"hooks": {
			"PreToolUse": [
				{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "/usr/local/bin/bash-guard-v2.sh" }] }
			]
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", tempSrc, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("re-install: %v\n%s", err, out.String())
	}

	after := readCodexHooks(t, configPath)
	if len(after["PreToolUse"]) != 1 {
		t.Fatalf("expected re-install to REPLACE (1 binding); got %d: %v", len(after["PreToolUse"]), after["PreToolUse"])
	}
	specs, _ := after["PreToolUse"][0]["hooks"].([]any)
	cmdStr, _ := specs[0].(map[string]any)["command"].(string)
	if cmdStr != "/usr/local/bin/bash-guard-v2.sh" {
		t.Errorf("expected re-installed v2 command; got %q", cmdStr)
	}
}

// TestInstall_HookOnCodex_TimeoutField_RoundTrip pins the integer
// round-trip (Go int → TOML int → int64) for the timeout field. The
// emit path passes spec.Timeout (Go int) into map[string]any; go-toml/
// v2 emits as `timeout = 30`; toml.Unmarshal reads back as int64(30);
// json.Marshal of both produces "30" so selectorFor matches.
//
// Closes advisor's probe-gap call-out (probe phase 5b proves the hash
// invariant; this test exercises it end-to-end).
func TestInstall_HookOnCodex_TimeoutField_RoundTrip(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()

	src := filepath.Join(t.TempDir(), "timeout-hook.hook.json")
	if err := os.WriteFile(src, []byte(`{
		"hooks": {
			"PreToolUse": [
				{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "/usr/local/bin/slow.sh", "timeout": 30 }] }
			]
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", codexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	configPath := filepath.Join(codexHome, "config.toml")
	raw, _ := os.ReadFile(configPath)
	asStr := string(raw)
	// Integer round-trip: `30`, not `30.0`. The normalizeForTOML
	// coercion path running at apply-time on mk.Value catches the
	// JSON-decoded float64(30) → int64(30) before go-toml/v2 emits.
	if !strings.Contains(asStr, "timeout = 30") {
		t.Errorf("expected `timeout = 30` in output; got:\n%s", asStr)
	}
	if strings.Contains(asStr, "timeout = 30.0") {
		t.Errorf("timeout rendered as float — normalizeForTOML didn't fire on mk.Value; got:\n%s", asStr)
	}

	// Round-trip uninstall: if hash didn't match, drift-tolerance no-op
	// would leave the element on disk.
	codexHookUninstall(t, "codex:hook:timeout-hook")
	after := readCodexHooks(t, configPath)
	if len(after["PreToolUse"]) != 0 {
		t.Errorf("expected timeout binding removed by uninstall; got %v", after["PreToolUse"])
	}
}

// TestInstall_HookAndMCPServerOnCodex_Coexist pins the codex-specific
// symmetry the claudecode adapter doesn't exercise: both kinds write
// to the same ~/.codex/config.toml file (claudecode splits hook →
// settings.json, mcp-server → .mcp.json into separate files). The
// orchestrator handles one install at a time so there's no apply-
// concurrent collision, but the file ends up carrying both
// [mcp_servers.foo] and [[hooks.PreToolUse]] tables — and each kind's
// uninstall must touch only its own kind's bytes.
//
// Closes advisor's gap #3.
func TestInstall_HookAndMCPServerOnCodex_Coexist(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()

	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", codexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	// Install mcp-server first.
	mcpSrc := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", mcpSrc, "--agent", "codex", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install mcp-server: %v", err)
	}

	// Install hook second.
	hookSrc := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")
	cmd = NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", hookSrc, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install hook: %v", err)
	}

	configPath := filepath.Join(codexHome, "config.toml")
	raw, _ := os.ReadFile(configPath)
	var parsed map[string]any
	if err := toml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	if servers, _ := parsed["mcp_servers"].(map[string]any); servers["github"] == nil {
		t.Errorf("mcp_servers.github should be present after hook install; got:\n%s", raw)
	}
	if hooks, _ := parsed["hooks"].(map[string]any); hooks["PreToolUse"] == nil {
		t.Errorf("hooks.PreToolUse should be present after both installs; got:\n%s", raw)
	}

	// Uninstall mcp-server; hook survives.
	codexHookUninstall(t, "codex:mcp-server:github")
	raw2, _ := os.ReadFile(configPath)
	var p2 map[string]any
	toml.Unmarshal(raw2, &p2)
	if servers, _ := p2["mcp_servers"].(map[string]any); servers["github"] != nil {
		t.Errorf("mcp_servers.github should be gone after its uninstall; got:\n%s", raw2)
	}
	if hooks, _ := p2["hooks"].(map[string]any); hooks["PreToolUse"] == nil {
		t.Errorf("hooks.PreToolUse must survive sibling-kind uninstall; got:\n%s", raw2)
	}

	// Uninstall hook; both gone (or mcp_servers preserved if empty
	// table left; either way, hook binding is gone).
	codexHookUninstall(t, "codex:hook:bash-guard")
	raw3, _ := os.ReadFile(configPath)
	var p3 map[string]any
	toml.Unmarshal(raw3, &p3)
	if hooks, _ := p3["hooks"].(map[string]any); hooks != nil {
		if arr, _ := hooks["PreToolUse"].([]any); len(arr) > 0 {
			for _, el := range arr {
				if b, ok := el.(map[string]any); ok && b["matcher"] == "Bash" {
					if specs, ok := b["hooks"].([]any); ok && len(specs) > 0 {
						if s, ok := specs[0].(map[string]any); ok && s["command"] == "/usr/local/bin/bash-guard.sh" {
							t.Errorf("expected hook binding gone after hook uninstall; got:\n%s", raw3)
						}
					}
				}
			}
		}
	}
}

// TestInstall_HookOnCodex_PreservesUserAuthoredFloatSyntax pins the
// hostile-review #1 invariant from slice v18 carried into the Op=Append
// path: a pre-existing user-authored `version = 1.0` at config.toml's
// top level must survive a hook install AND a hook uninstall. Mirror
// of the same-named mcp-server test; the protection lives in writeTOML
// (no whole-root normalize) so it covers both Op=Set and Op=Append
// automatically. Without this test a future refactor that moves
// normalizeForTOML back to writeTOML would silently regress on
// Op=Append too.
//
// Uninstall side pinned because writeTOML runs at both install
// (apply) and uninstall (un-merge) write-back time; symmetric coverage
// catches asymmetric regression risk.
func TestInstall_HookOnCodex_PreservesUserAuthoredFloatSyntax(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()

	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(`version = 1.0
profile = "default"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")

	raw, _ := os.ReadFile(configPath)
	if !strings.Contains(string(raw), "version = 1.0") {
		t.Errorf("user-authored `version = 1.0` must survive hook install; got:\n%s", raw)
	}

	codexHookUninstall(t, "codex:hook:bash-guard")
	raw2, _ := os.ReadFile(configPath)
	if !strings.Contains(string(raw2), "version = 1.0") {
		t.Errorf("user-authored `version = 1.0` must survive hook uninstall; got:\n%s", raw2)
	}
}

// TestInstall_HookOnCodex_OrderOfInstallsPreserved pins that two
// separate installs into the same hooks.<Event> array land in install
// order on disk — hook execution order = install order. The append-
// not-set design exists for this invariant. Mirror of the same-named
// claudecode test; codex-specific because the JSON-side test exercises
// json.Marshal append, this exercises go-toml/v2 Marshal of a multi-
// element []any (the canonical [[hooks.<Event>]] array-of-tables on
// successive append).
func TestInstall_HookOnCodex_OrderOfInstallsPreserved(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	configPath := codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")

	// Install a second hook (different name = different ID) into the
	// same event. Temp source file so we don't depend on testdata fixture
	// layout for a one-shot.
	tempSrc := filepath.Join(t.TempDir(), "edit-guard.hook.json")
	if err := os.WriteFile(tempSrc, []byte(`{
		"hooks": {
			"PreToolUse": [
				{ "matcher": "Edit", "hooks": [{ "type": "command", "command": "/usr/local/bin/edit-guard.sh" }] }
			]
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", tempSrc, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install 2: %v\n%s", err, out.String())
	}

	after := readCodexHooks(t, configPath)
	if len(after["PreToolUse"]) != 2 {
		t.Fatalf("expected 2 bindings; got %d", len(after["PreToolUse"]))
	}
	if after["PreToolUse"][0]["matcher"] != "Bash" {
		t.Errorf("first binding should be Bash (installed first); got %v", after["PreToolUse"][0]["matcher"])
	}
	if after["PreToolUse"][1]["matcher"] != "Edit" {
		t.Errorf("second binding should be Edit (installed second); got %v", after["PreToolUse"][1]["matcher"])
	}

	// On-disk shape: two separate [[hooks.PreToolUse]] tables (NOT a
	// collapsed inline array). Pins the canonical AOT emit on multi-
	// element arrays — the failure mode would be go-toml/v2 emitting
	// `hooks.PreToolUse = [{...}, {...}]` inline, which is parseable
	// but visually surprising to a user diffing the file.
	raw, _ := os.ReadFile(configPath)
	if strings.Count(string(raw), "[[hooks.PreToolUse]]") != 2 {
		t.Errorf("expected two [[hooks.PreToolUse]] tables on disk; got:\n%s", raw)
	}
}

// TestInstall_HookOnCodex_DuplicateContent_DifferentNameRefused pins
// the preflight invariant for byte-identical sibling appends on TOML:
// two installs under different IDs producing identical bindings would
// tangle at uninstall (first hash match wins, removing the wrong
// install's entry). preflightMergedKeyCollisions is format-agnostic so
// it should fire identically on TOML and JSON — this test pins the
// codex-side surface.
//
// Mirror of TestInstall_Hook_DuplicateContent_DifferentNameRefused on
// claudecode. Without this test the codex-side preflight could
// silently regress and the failure mode would be invisible manifest
// corruption.
func TestInstall_HookOnCodex_DuplicateContent_DifferentNameRefused(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")

	// Identical content under a different filename → different install
	// ID, same content-hash.
	tempSrc := filepath.Join(t.TempDir(), "bash-guard-clone.hook.json")
	body, err := os.ReadFile(filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tempSrc, body, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", tempSrc, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected byte-identical sibling install to refuse; got success\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "byte-identical") {
		t.Errorf("expected byte-identical collision message; got %v", err)
	}
}

// TestInstall_HookOnCodex_FileModePreservation pins the mode-
// preservation contract for codex hook on TOML. config.toml may carry
// credentials in mcp_servers env or in user-authored sibling sections;
// a pre-existing 0o600 must survive.
func TestInstall_HookOnCodex_FileModePreservation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode preservation is POSIX-specific")
	}
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte(`profile = "private"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	codexHookInstallHelper(t, codexHome, dotpackHome, "bash-guard.hook.json")

	st, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0o600 preserved after hook install; got %o", perm)
	}

	codexHookUninstall(t, "codex:hook:bash-guard")
	st2, _ := os.Stat(configPath)
	if perm := st2.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0o600 preserved after hook uninstall; got %o", perm)
	}
}

// TestInstall_HookOnCodex_SymlinkRefused pins the symlink defense for
// codex hook on TOML. The defense lives in orchestrator.preflightMerged
// KeyCollisions (format-agnostic) so the same code path catches
// settings.json AND config.toml symlinks.
func TestInstall_HookOnCodex_SymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; not exercised on this platform")
	}
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()

	configPath := filepath.Join(codexHome, "config.toml")
	target := filepath.Join(t.TempDir(), "real-config.toml")
	if err := os.WriteFile(target, []byte("# real codex config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", codexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected install to refuse symlink at %s; got success\n%s", configPath, out.String())
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink-collision error; got %v", err)
	}
}

// TestInstall_HookOnCodex_RejectsLegacyFlatHooksShape pins the
// schema's ecosystem_notes warning about in-the-wild non-spec TOML:
// `[hooks]\nPreToolUse = "/path/x.sh"` (flat key-value map) shape was
// observed in vbcherepanov/total-agent-memory. dotpack's appendJSONPath
// errors when the leaf is a non-array — the user gets a structured
// "refusing to overwrite (manually edit the file or use --force)"
// rather than dotpack silently coercing their string into an array.
//
// Closes advisor's gap #4. The error surface tests preflight or apply-
// time depending on whether the slot is occupied by something non-array.
func TestInstall_HookOnCodex_RejectsLegacyFlatHooksShape(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()

	configPath := filepath.Join(codexHome, "config.toml")
	// Legacy non-spec shape: flat key-value (NOT array-of-tables). When
	// dotpack tries to append into hooks.PreToolUse, the leaf is a
	// string — appendJSONPath errors structured. Pin the error surface.
	if err := os.WriteFile(configPath, []byte(`[hooks]
PreToolUse = "/legacy/non-spec.sh"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", codexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected install to refuse legacy non-array PreToolUse; got success\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "non-array at array path") && !strings.Contains(err.Error(), "not an array") {
		t.Errorf("expected structured 'non-array' error; got %v", err)
	}

	// File on disk untouched — the user's legacy entry survives the
	// refusal.
	raw, _ := os.ReadFile(configPath)
	if !strings.Contains(string(raw), `PreToolUse = "/legacy/non-spec.sh"`) {
		t.Errorf("user's legacy entry must survive the refusal; got:\n%s", raw)
	}
}

