package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// hookInstallHelper installs the canonical bash-guard hook into the
// supplied dirs at project scope and returns the resolved settings.json
// path. Used by the scenario tests below so the boilerplate
// env-setup + cmd-invocation lives in one place.
func hookInstallHelper(t *testing.T, projectHome, claudeHome, dotpackHome, srcName string) string {
	t.Helper()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "hooks", srcName)
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "hook", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	return filepath.Join(projectHome, ".claude", "settings.json")
}

func hookUninstall(t *testing.T, id string) {
	t.Helper()
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"uninstall", id})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("uninstall %s: %v", id, err)
	}
}

// readHooks parses settings.json into the standard event → []binding
// shape so each test can assert on its specific slice.
func readHooks(t *testing.T, path string) map[string][]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse %s: %v", path, err)
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

// TestInstall_Hook_SiblingKeyPreservation_AcrossInstallUninstall pins
// advisor implicit constraint #1: a pre-existing settings.json with
// permissions, env, sibling user-hand-installed $.hooks.SessionStart,
// AND $.hooks.PreToolUse entries authored by the user must survive a
// dotpack install + uninstall round trip with their non-dotpack bytes
// intact. This is the "settings.json is user-owned, not dotpack-owned"
// contract — get it wrong and dotpack would silently rewrite the user's
// permissions or trample their hand-installed hooks.
func TestInstall_Hook_SiblingKeyPreservation_AcrossInstallUninstall(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()

	// Pre-existing settings.json with user permissions, env, AND
	// hand-authored hooks on both the same event (PreToolUse) and a
	// different event (SessionStart).
	existing := map[string]any{
		"permissions": map[string]any{"allow": []any{"Edit", "Read"}},
		"env":         map[string]any{"FOO": "bar"},
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{map[string]any{
						"type":    "command",
						"command": "/usr/local/bin/welcome.sh",
					}},
				},
			},
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Read",
					"hooks": []any{map[string]any{
						"type":    "command",
						"command": "/usr/local/bin/read-guard.sh",
					}},
				},
			},
		},
	}
	settingsPath := filepath.Join(projectHome, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	pre, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, pre, 0o644); err != nil {
		t.Fatal(err)
	}

	got := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")
	if got != settingsPath {
		t.Fatalf("unexpected settings path: %s", got)
	}

	// After install: permissions + env unchanged; SessionStart still
	// has the user's binding; PreToolUse has BOTH the user's read-guard
	// AND the dotpack-installed bash-guard.
	afterInstall, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var post map[string]any
	if err := json.Unmarshal(afterInstall, &post); err != nil {
		t.Fatal(err)
	}
	if perms, _ := post["permissions"].(map[string]any); perms == nil {
		t.Errorf("permissions disappeared after install: %s", afterInstall)
	} else if allow, _ := perms["allow"].([]any); len(allow) != 2 || allow[0] != "Edit" {
		t.Errorf("permissions.allow mutated: %v", allow)
	}
	if envMap, _ := post["env"].(map[string]any); envMap == nil || envMap["FOO"] != "bar" {
		t.Errorf("env mutated/missing: %v", post["env"])
	}
	hooks := readHooks(t, settingsPath)
	if len(hooks["SessionStart"]) != 1 || hooks["SessionStart"][0]["hooks"].([]any)[0].(map[string]any)["command"] != "/usr/local/bin/welcome.sh" {
		t.Errorf("SessionStart binding mutated: %v", hooks["SessionStart"])
	}
	if len(hooks["PreToolUse"]) != 2 {
		t.Fatalf("expected PreToolUse to contain user-read-guard + dotpack-bash-guard; got %d bindings: %v", len(hooks["PreToolUse"]), hooks["PreToolUse"])
	}

	// Uninstall: dotpack's bash-guard goes; user's read-guard stays;
	// permissions + env + SessionStart untouched.
	hookUninstall(t, "claude-code:hook:bash-guard")
	hooksAfter := readHooks(t, settingsPath)
	if len(hooksAfter["PreToolUse"]) != 1 {
		t.Fatalf("expected user's read-guard to survive; got %d bindings: %v", len(hooksAfter["PreToolUse"]), hooksAfter["PreToolUse"])
	}
	if hooksAfter["PreToolUse"][0]["matcher"] != "Read" {
		t.Errorf("surviving binding is not the user's read-guard: %v", hooksAfter["PreToolUse"][0])
	}
	if len(hooksAfter["SessionStart"]) != 1 {
		t.Errorf("SessionStart binding lost during uninstall: %v", hooksAfter["SessionStart"])
	}
	finalRaw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var final map[string]any
	if err := json.Unmarshal(finalRaw, &final); err != nil {
		t.Fatal(err)
	}
	if perms, _ := final["permissions"].(map[string]any); perms == nil {
		t.Errorf("permissions disappeared after uninstall: %s", finalRaw)
	}
	if envMap, _ := final["env"].(map[string]any); envMap == nil || envMap["FOO"] != "bar" {
		t.Errorf("env disappeared after uninstall: %v", final["env"])
	}
}

// TestInstall_Hook_UserEdit_DriftTolerantUninstall pins advisor user-
// edit scenario #2: when the user edits dotpack's installed binding,
// uninstall's content-hash scan no longer matches — the function
// no-ops on the array (leaves the user's edit alone) but still removes
// the manifest record. "Drift on uninstall is intentional" extended
// from the mcp-server slice to hook's array case.
func TestInstall_Hook_UserEdit_DriftTolerantUninstall(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	// Mutate the dotpack-installed binding's command.
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	pre := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	pre[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"] = "/usr/local/bin/USER-EDITED.sh"
	mutated, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(settingsPath, mutated, 0o644); err != nil {
		t.Fatal(err)
	}

	hookUninstall(t, "claude-code:hook:bash-guard")

	// User's edit survived (drift tolerance).
	after := readHooks(t, settingsPath)
	if len(after["PreToolUse"]) != 1 {
		t.Fatalf("expected user-edited binding to survive uninstall; got %d", len(after["PreToolUse"]))
	}
	specs, _ := after["PreToolUse"][0]["hooks"].([]any)
	cmd, _ := specs[0].(map[string]any)["command"].(string)
	if cmd != "/usr/local/bin/USER-EDITED.sh" {
		t.Errorf("expected USER-EDITED command to survive; got %q", cmd)
	}

	// Manifest record removed even though file un-merge no-op'd.
	mr, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mr), "claude-code:hook:bash-guard") {
		t.Errorf("expected manifest record removed; got %s", mr)
	}
}

// TestInstall_Hook_UserReorder_HashIdentitySurvives pins advisor user-
// edit scenario #3: the content-hash identity survives even when the
// user reorders array elements. Numeric-index would fail this scenario
// because the install's recorded index would point at the wrong element
// post-reorder.
func TestInstall_Hook_UserReorder_HashIdentitySurvives(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	// Inject a sibling at position 0, pushing dotpack's binding to
	// position 1 — exactly the reorder a numeric-index scheme would
	// trip over.
	raw, _ := os.ReadFile(settingsPath)
	var root map[string]any
	json.Unmarshal(raw, &root)
	pre := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	sibling := map[string]any{
		"matcher": "Edit",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": "/usr/local/bin/edit-sibling.sh",
		}},
	}
	root["hooks"].(map[string]any)["PreToolUse"] = append([]any{sibling}, pre...)
	out, _ := json.MarshalIndent(root, "", "  ")
	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		t.Fatal(err)
	}

	hookUninstall(t, "claude-code:hook:bash-guard")

	after := readHooks(t, settingsPath)
	if len(after["PreToolUse"]) != 1 {
		t.Fatalf("expected sibling to survive; got %d", len(after["PreToolUse"]))
	}
	if after["PreToolUse"][0]["matcher"] != "Edit" {
		t.Errorf("expected sibling matcher=Edit to survive; got %v", after["PreToolUse"][0])
	}
}

// TestInstall_Hook_UserDeleted_IdempotentUninstall pins advisor user-
// edit scenario #4: when the user has already deleted the dotpack
// binding from settings.json by hand, uninstall is a no-op on the file
// but still removes the manifest record.
func TestInstall_Hook_UserDeleted_IdempotentUninstall(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	// User deletes the PreToolUse array entirely.
	raw, _ := os.ReadFile(settingsPath)
	var root map[string]any
	json.Unmarshal(raw, &root)
	delete(root["hooks"].(map[string]any), "PreToolUse")
	out, _ := json.MarshalIndent(root, "", "  ")
	os.WriteFile(settingsPath, out, 0o644)

	hookUninstall(t, "claude-code:hook:bash-guard")

	mr, _ := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if strings.Contains(string(mr), "claude-code:hook:bash-guard") {
		t.Errorf("manifest record should be gone: %s", mr)
	}
}

// TestInstall_Hook_ReInstall_ReplacesNotDuplicates pins the re-install
// contract for Op=Append: installing the same ID a second time (with
// different content) must REPLACE the old array element, not append
// alongside it. Without unmergeExistingAppendsForID the array would
// grow on every re-install.
func TestInstall_Hook_ReInstall_ReplacesNotDuplicates(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	// Edit the source's command, then re-install under the same name.
	// Writing a sibling testdata file would couple this test to disk
	// layout — instead, override the source from a temp file.
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
	cmd.SetArgs([]string{"install", tempSrc, "--agent", "claude-code", "--kind", "hook", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("re-install: %v\n%s", err, out.String())
	}

	after := readHooks(t, settingsPath)
	if len(after["PreToolUse"]) != 1 {
		t.Fatalf("expected re-install to REPLACE (1 binding); got %d: %v", len(after["PreToolUse"]), after["PreToolUse"])
	}
	specs, _ := after["PreToolUse"][0]["hooks"].([]any)
	cmdStr, _ := specs[0].(map[string]any)["command"].(string)
	if cmdStr != "/usr/local/bin/bash-guard-v2.sh" {
		t.Errorf("expected re-installed v2 command; got %q", cmdStr)
	}
}

// TestInstall_Hook_OrderOfInstallsPreserved pins advisor constraint #2:
// hook execution order = install order. Walker appends — never inserts —
// so two sequential installs to the same event produce array elements
// in install order on disk.
func TestInstall_Hook_OrderOfInstallsPreserved(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	// Install a second hook (different name = different ID) into the
	// same event.
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
	cmd.SetArgs([]string{"install", tempSrc, "--agent", "claude-code", "--kind", "hook", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install 2: %v\n%s", err, out.String())
	}

	after := readHooks(t, settingsPath)
	if len(after["PreToolUse"]) != 2 {
		t.Fatalf("expected 2 bindings; got %d", len(after["PreToolUse"]))
	}
	if after["PreToolUse"][0]["matcher"] != "Bash" {
		t.Errorf("first binding should be Bash (installed first); got %v", after["PreToolUse"][0]["matcher"])
	}
	if after["PreToolUse"][1]["matcher"] != "Edit" {
		t.Errorf("second binding should be Edit (installed second); got %v", after["PreToolUse"][1]["matcher"])
	}
}

// TestInstall_Hook_FileModePreservation pins advisor constraint #3:
// settings.json may carry credentials in hook env or command strings;
// a pre-existing 0o600 file must keep 0o600 across install+uninstall.
// Same posture as mcp-server's .mcp.json mode-preservation guard.
func TestInstall_Hook_FileModePreservation(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()

	settingsPath := filepath.Join(projectHome, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing file at 0o600 with a sibling key.
	if err := os.WriteFile(settingsPath, []byte(`{"permissions":{"allow":["Edit"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	st, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0o600 preserved after install; got %o", perm)
	}

	hookUninstall(t, "claude-code:hook:bash-guard")

	st2, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st2.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0o600 preserved after uninstall; got %o", perm)
	}
}

// TestInstall_Hook_SymlinkRefused pins advisor constraint #4: symlink
// defense at preflight covers settings.json too. Without this, the
// merged-key apply path would resolve the symlink and rewrite the
// target's path while leaving the symlink-named entry untouched —
// silently moving the user's data.
func TestInstall_Hook_SymlinkRefused(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()

	settingsPath := filepath.Join(projectHome, ".claude", "settings.json")
	target := filepath.Join(t.TempDir(), "real-settings.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, settingsPath); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "hook", "--scope", "project"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected install to refuse symlink at %s; got success\n%s", settingsPath, out.String())
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink-collision error; got %v", err)
	}
}

// TestInstall_Hook_UserScope pins user-scope installs land at
// <ClaudeHome>/settings.json (NOT inside a .claude subdir — ClaudeHome
// IS the .claude tree). This is the asymmetry with mcp-server's user
// scope (~/.claude.json is OUTSIDE ClaudeHome) that lets hook wire
// user-scope from day 1.
func TestInstall_Hook_UserScope(t *testing.T) {
	claudeHome := t.TempDir()
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}

	userSettings := filepath.Join(claudeHome, "settings.json")
	if _, err := os.Stat(userSettings); err != nil {
		t.Fatalf("expected user-scope settings.json at %s: %v", userSettings, err)
	}
	hooks := readHooks(t, userSettings)
	if len(hooks["PreToolUse"]) != 1 {
		t.Errorf("expected user-scope install to land at %s; got hooks=%v", userSettings, hooks)
	}
	// Project-scope file MUST NOT have been created.
	projectSettings := filepath.Join(projectHome, ".claude", "settings.json")
	if _, err := os.Stat(projectSettings); !errorsIsNotExist(err) {
		t.Errorf("user-scope install must not write project-scope file %s (err=%v)", projectSettings, err)
	}
}

// TestInstall_Hook_DuplicateContent_DifferentNameRefused pins the
// preflight invariant for byte-identical sibling appends: two installs
// under different IDs producing identical bindings would tangle at
// uninstall (first hash match wins, removing the wrong install's
// entry). Preflight refuses; --force is the documented escape hatch.
func TestInstall_Hook_DuplicateContent_DifferentNameRefused(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	// Identical content under a different filename → different
	// install ID, same content-hash.
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
	cmd.SetArgs([]string{"install", tempSrc, "--agent", "claude-code", "--kind", "hook", "--scope", "project"})
	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected byte-identical sibling install to refuse; got success\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "byte-identical") {
		t.Errorf("expected byte-identical collision message; got %v", err)
	}
}

// TestInstall_Hook_MultiBinding_RoundTrip pins ADR-0016 §9's "Multiple
// keys per hook install (one per binding leaf path)" — the case that
// drove configfrag.KindConfig.Emit to return []MergedFragment in the
// mcp-server slice. One resource installs 3 bindings across 2 events
// (PreToolUse × 2 + PostToolUse × 1). The manifest must record 3
// MergedKey entries (each with its own Selector); install writes all
// 3 to settings.json; uninstall removes all 3 in one go and the
// manifest record disappears.
//
// Without this test the configfrag-slice contract is unverified
// end-to-end — the tracer only pins N=1, leaving N>1 unproven.
// Hostile-review finding #2.
func TestInstall_Hook_MultiBinding_RoundTrip(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "multi-binding.hook.json")

	hooks := readHooks(t, settingsPath)
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
		t.Errorf("PreToolUse matcher order = %v; want [Bash, Edit|Write] (emit preserves source order within event)", matchers)
	}

	// Manifest must record 3 separate MergedKey entries — one per
	// binding-leaf path per ADR-0016 §9. Each gets its own Selector
	// (content-hash); the orchestrator's deterministic sort by
	// (File, Path, Selector) keeps the manifest stable for
	// `dotpack list` rendering.
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
	// All entries Op=append + non-empty Selector. Selectors must be
	// pairwise distinct — three different binding contents.
	seen := map[string]struct{}{}
	for i, mk := range m.Installs[0].MergedKeys {
		if mk.Op != "append" {
			t.Errorf("merged_keys[%d].op = %q; want append", i, mk.Op)
		}
		if mk.Selector == "" {
			t.Errorf("merged_keys[%d].selector is empty", i)
		}
		if _, dup := seen[mk.Selector]; dup {
			t.Errorf("merged_keys[%d].selector %q duplicates an earlier entry — binding contents must hash distinctly", i, mk.Selector)
		}
		seen[mk.Selector] = struct{}{}
	}

	// Uninstall: all 3 bindings gone, both event arrays empty (but
	// retained as keys), manifest record removed.
	hookUninstall(t, "claude-code:hook:multi-binding")
	after := readHooks(t, settingsPath)
	if len(after["PreToolUse"]) != 0 {
		t.Errorf("expected PreToolUse to be empty after uninstall; got %d", len(after["PreToolUse"]))
	}
	if len(after["PostToolUse"]) != 0 {
		t.Errorf("expected PostToolUse to be empty after uninstall; got %d", len(after["PostToolUse"]))
	}
	mr2, _ := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if strings.Contains(string(mr2), "claude-code:hook:multi-binding") {
		t.Errorf("expected manifest record removed; got %s", mr2)
	}
}

// TestInstall_Hook_EnvField_HashStableInstallToUninstall pins the
// env-field round-trip: emit serialises env as map[string]string;
// uninstall re-reads the file back into map[string]any (with string
// values). Both json.Marshal paths must produce byte-identical bytes
// for selectorFor to match across install and uninstall. Without
// this, the env-bearing hook would install but fail to un-merge
// (drift-tolerant no-op) — a silent regression undetectable by the
// env-free tracer. Hostile-review #4 follow-on test.
func TestInstall_Hook_EnvField_HashStableInstallToUninstall(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "env-hook.hook.json")

	// Install OK; env appears at the right place + with the right
	// shape (string→string, not nested any).
	hooks := readHooks(t, settingsPath)
	if len(hooks["PreToolUse"]) != 1 {
		t.Fatalf("expected 1 binding; got %d", len(hooks["PreToolUse"]))
	}
	specs, _ := hooks["PreToolUse"][0]["hooks"].([]any)
	envMap, _ := specs[0].(map[string]any)["env"].(map[string]any)
	if envMap["SECRET_TOKEN"] != "shh" || envMap["TIER"] != "prod" {
		t.Errorf("env not preserved correctly: %v", envMap)
	}

	// Uninstall: if the hash mismatched, the array element would
	// survive and the test would fail. Drift-tolerance no-op only
	// fires when the user has edited the binding — here we have NOT
	// edited it, so the hash must match for the element to be
	// removed.
	hookUninstall(t, "claude-code:hook:env-hook")
	after := readHooks(t, settingsPath)
	if len(after["PreToolUse"]) != 0 {
		t.Errorf("expected env-bearing binding to be removed by uninstall (hash must match install side); got %v", after["PreToolUse"])
	}
}

// TestInstall_Hook_ReInstall_SymlinkHijack_StillRefused pins the
// hostile-review #1 fix: symlink defense fires regardless of same-ID
// re-install. Without the fix, a user could install hook A normally,
// then replace settings.json with a symlink to /etc/something, then
// re-install hook A → writeAtomic's rename would silently rewrite
// through the symlink. The fix decouples the symlink check from the
// same-ID short-circuit in preflightMergedKeyCollisions.
func TestInstall_Hook_ReInstall_SymlinkHijack_StillRefused(t *testing.T) {
	projectHome := t.TempDir()
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	settingsPath := hookInstallHelper(t, projectHome, claudeHome, dotpackHome, "bash-guard.hook.json")

	// User replaces the freshly-installed settings.json with a symlink.
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(settingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, settingsPath); err != nil {
		t.Fatal(err)
	}

	// Re-install the same hook (same ID). Without the fix, preflight's
	// same-ID short-circuit would skip the symlink check; with the fix,
	// the symlink check runs first and the re-install refuses.
	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "hook", "--scope", "project"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected re-install to refuse symlink hijack; got success\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink-collision error on re-install; got %v", err)
	}
}

// errorsIsNotExist is a thin wrapper so the scenario tests can phrase
// the "no such file" predicate inline without each repeating
// errors.Is. os.Stat returns a *os.PathError whose underlying err is
// fs.ErrNotExist on missing files; errors.Is unwraps correctly.
func errorsIsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
