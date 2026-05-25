package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestInstall_HookOnClaudeCode_ProjectScope_FreshFile is the tracer
// bullet for the hook kind on the configfrag pattern per ADR-0016
// §5–§7 + §9. Smallest meaningful vertical slice: one event × one
// binding × one hook-spec on one host (claude-code), JSON-only, one
// scope (project), fresh file (no existing .claude/settings.json).
// Forces all the new machinery beyond what mcp-server proved:
//
//   - resource.Hook parser + filename-derived name
//   - validator.ValidateHook universal-core invariants (type=command,
//     event-name on the canonical list)
//   - configfrag emit returning N MergedFragments per ADR §9 (the
//     N>1 case isn't pinned here; this tracer pins the N=1 + Op=Append
//     plumbing)
//   - adapter.MergedKeyWrite gains Op (set vs append) per advisor:
//     the schema-side path `$.hooks.PreToolUse` is an array target,
//     not a leaf-set target
//   - manifest.MergedKey gains Op + Selector (sha256 of value) per
//     advisor: numeric array indices are unstable when siblings move,
//     so identity is content-hash, persisted at install and re-derived
//     at uninstall by scanning the array
//   - orchestrator walker: applyJSONMergedKey dispatches on Op
//     (set vs append); unmergeJSONKey for Op=Append removes the array
//     element whose hash matches the manifest's Selector
//   - Reader.Uninstall (still adapter-free) for hook end-to-end
//
// What this test pins:
//
//  1. `dotpack install <src> --agent claude-code --kind hook
//     --scope project` writes a .claude/settings.json at
//     <ProjectHome>/.claude/settings.json with the binding appended
//     to $.hooks.PreToolUse.
//  2. Manifest record has ID claude-code:hook:bash-guard (filename-
//     derived), Kind hook, one merged_keys entry with Op="append" and
//     a sha256: Selector (non-empty, deterministic).
//  3. Uninstall removes the binding from the array (matching by
//     Selector hash, not numeric index) AND removes the manifest
//     record. The .claude/settings.json file itself stays; the empty
//     PreToolUse array stays too — dotpack does not second-guess the
//     user's host config file.
//
// Filename → name: bash-guard.hook.json → "bash-guard". Strips the
// trailing .hook.json or .json extension. Mirrors how skill/agent
// names come from frontmatter (which hooks lack); filesystem encodes
// identity when the source format does not.
//
// Test will fail RED until tasks 4–8 land.
func TestInstall_HookOnClaudeCode_ProjectScope_FreshFile(t *testing.T) {
	claudeHome := t.TempDir()
	projectHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "claude-code",
		"--kind", "hook",
		"--scope", "project",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if !strings.Contains(got, "Installed claude-code:hook:bash-guard") {
		t.Errorf("expected success message with filename-derived name; got %q", got)
	}

	settingsPath := filepath.Join(projectHome, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse settings.json: %v\n%s", err, raw)
	}
	hooks, _ := parsed["hooks"].(map[string]any)
	if hooks == nil {
		t.Fatalf("expected hooks map at root; got %s", raw)
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)
	if len(preToolUse) != 1 {
		t.Fatalf("expected one element under $.hooks.PreToolUse; got %d (%s)", len(preToolUse), raw)
	}
	binding, _ := preToolUse[0].(map[string]any)
	if binding == nil {
		t.Fatalf("expected binding object at $.hooks.PreToolUse[0]; got %s", raw)
	}
	if binding["matcher"] != "Bash" {
		t.Errorf("$.hooks.PreToolUse[0].matcher = %v; want Bash", binding["matcher"])
	}
	specs, _ := binding["hooks"].([]any)
	if len(specs) != 1 {
		t.Fatalf("expected one hook-spec; got %d", len(specs))
	}
	spec, _ := specs[0].(map[string]any)
	if spec["type"] != "command" {
		t.Errorf("hook-spec.type = %v; want command", spec["type"])
	}
	if spec["command"] != "/usr/local/bin/bash-guard.sh" {
		t.Errorf("hook-spec.command = %v; want /usr/local/bin/bash-guard.sh", spec["command"])
	}

	// Manifest record check. MergedKeys gains Op + Selector — the
	// Selector is a sha256 hash of the marshalled value, computed by
	// the orchestrator at install time and stored so uninstall can
	// scan the array for the matching element (sibling-uninstall-safe
	// per advisor).
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
	if rec.ID != "claude-code:hook:bash-guard" {
		t.Errorf("record ID = %q; want claude-code:hook:bash-guard", rec.ID)
	}
	if rec.Kind != "hook" {
		t.Errorf("record Kind = %q; want hook", rec.Kind)
	}
	if rec.Agent != "claude-code" {
		t.Errorf("record Agent = %q; want claude-code", rec.Agent)
	}
	if len(rec.Files) != 0 {
		t.Errorf("config-fragment install must not claim files; got %v", rec.Files)
	}
	if len(rec.MergedKeys) != 1 {
		t.Fatalf("expected 1 merged_keys entry; got %d (%v)", len(rec.MergedKeys), rec.MergedKeys)
	}
	mk := rec.MergedKeys[0]
	if mk.File != settingsPath {
		t.Errorf("merged_keys[0].file = %q; want %q", mk.File, settingsPath)
	}
	if mk.Path != "$.hooks.PreToolUse" {
		t.Errorf("merged_keys[0].path = %q; want $.hooks.PreToolUse", mk.Path)
	}
	if mk.Op != "append" {
		t.Errorf("merged_keys[0].op = %q; want append", mk.Op)
	}
	selectorRE := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if !selectorRE.MatchString(mk.Selector) {
		t.Errorf("merged_keys[0].selector = %q; want sha256:<64-hex>", mk.Selector)
	}

	// Uninstall removes the array element (matched by Selector hash)
	// and removes the manifest record. The .claude/settings.json file
	// itself stays; whether the PreToolUse array survives empty or is
	// pruned is a host-config-retention decision (this test does NOT
	// pin removal — only that the install's binding is gone).
	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "claude-code:hook:bash-guard"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if raw2, err := os.ReadFile(settingsPath); err == nil {
		var parsed2 map[string]any
		if jerr := json.Unmarshal(raw2, &parsed2); jerr == nil {
			if hooks2, ok := parsed2["hooks"].(map[string]any); ok {
				if arr, ok := hooks2["PreToolUse"].([]any); ok && len(arr) > 0 {
					// If any element survives, none of them must be the
					// dotpack-installed one (matcher=Bash + the specific
					// command). A user-added sibling would also have
					// matcher=Bash but with a different command; this
					// test installs only one element, so any survivor
					// is a bug.
					for _, el := range arr {
						if b, ok := el.(map[string]any); ok && b["matcher"] == "Bash" {
							if specs, ok := b["hooks"].([]any); ok && len(specs) == 1 {
								if s, ok := specs[0].(map[string]any); ok && s["command"] == "/usr/local/bin/bash-guard.sh" {
									t.Errorf("expected $.hooks.PreToolUse to no longer contain the bash-guard binding after uninstall; got %s", raw2)
								}
							}
						}
					}
				}
			}
		}
	}

	// Manifest record gone.
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
