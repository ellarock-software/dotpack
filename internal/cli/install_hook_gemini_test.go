package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func setupGeminiHookEnv(t *testing.T) (geminiHome, projectHome, dotpackHome string) {
	t.Helper()
	geminiHome = t.TempDir()
	projectHome = t.TempDir()
	dotpackHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	return geminiHome, projectHome, dotpackHome
}

func geminiHookInstallHelper(t *testing.T, geminiHome, projectHome, dotpackHome, srcName string, scope string) string {
	t.Helper()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	src := filepath.Join("..", "resource", "testdata", "hooks", srcName)
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--kind", "hook", "--scope", scope})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install %s: %v\n%s", srcName, err, out.String())
	}
	if scope == "project" {
		return filepath.Join(projectHome, ".gemini", "settings.json")
	}
	return filepath.Join(geminiHome, "settings.json")
}

func geminiHookUninstall(t *testing.T, id string) {
	t.Helper()
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"uninstall", id})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("uninstall %s: %v", id, err)
	}
}

func readGeminiHooks(t *testing.T, path string) map[string][]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
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

func TestInstall_HookOnGeminiCLI_UserScope_FreshFile(t *testing.T) {
	geminiHome, projectHome, dotpackHome := setupGeminiHookEnv(t)
	settingsPath := geminiHookInstallHelper(t, geminiHome, projectHome, dotpackHome, "bash-guard.hook.json", "user")

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse settings.json: %v\n%s", err, raw)
	}
	if _, exists := root["enabled"]; exists {
		t.Fatalf("gemini hook emit must not write legacy top-level enabled registry; got %s", raw)
	}

	hooks := readGeminiHooks(t, settingsPath)
	if len(hooks["BeforeTool"]) != 1 {
		t.Fatalf("expected one BeforeTool binding; got %d", len(hooks["BeforeTool"]))
	}
	binding := hooks["BeforeTool"][0]
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
	if spec["name"] != "bash-guard" {
		t.Errorf("gemini hook-spec should get a stable name from the dotpack hook name; got %v", spec["name"])
	}

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
	if rec.ID != "gemini-cli:hook:bash-guard" {
		t.Errorf("record ID = %q; want gemini-cli:hook:bash-guard", rec.ID)
	}
	if rec.Kind != "hook" {
		t.Errorf("record Kind = %q; want hook", rec.Kind)
	}
	if rec.Agent != "gemini-cli" {
		t.Errorf("record Agent = %q; want gemini-cli", rec.Agent)
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
	if mk.Path != "$.hooks.BeforeTool" {
		t.Errorf("merged_keys[0].path = %q; want $.hooks.BeforeTool", mk.Path)
	}
	if mk.Op != "append" {
		t.Errorf("merged_keys[0].op = %q; want append", mk.Op)
	}
	selectorRE := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if !selectorRE.MatchString(mk.Selector) {
		t.Errorf("merged_keys[0].selector = %q; want sha256:<64-hex>", mk.Selector)
	}

	geminiHookUninstall(t, "gemini-cli:hook:bash-guard")
	hooksAfter := readGeminiHooks(t, settingsPath)
	if len(hooksAfter["BeforeTool"]) != 0 {
		t.Errorf("expected BeforeTool binding removed after uninstall; got %v", hooksAfter["BeforeTool"])
	}
}

func TestInstall_HookOnGeminiCLI_ProjectScope_FreshFile(t *testing.T) {
	geminiHome, projectHome, dotpackHome := setupGeminiHookEnv(t)
	settingsPath := geminiHookInstallHelper(t, geminiHome, projectHome, dotpackHome, "bash-guard.hook.json", "project")
	wantPath := filepath.Join(projectHome, ".gemini", "settings.json")
	if settingsPath != wantPath {
		t.Fatalf("settings path = %q; want %q", settingsPath, wantPath)
	}
	hooks := readGeminiHooks(t, settingsPath)
	if len(hooks["BeforeTool"]) != 1 {
		t.Fatalf("expected project-scope BeforeTool binding; got %v", hooks)
	}
}

func TestInstall_HookOnGeminiCLI_EventAliasesAndTimeoutMilliseconds(t *testing.T) {
	geminiHome, _, _ := setupGeminiHookEnv(t)

	src := filepath.Join(t.TempDir(), "tool-timeouts.hook.json")
	if err := os.WriteFile(src, []byte(`{
		"hooks": {
			"PreToolUse": [
				{"matcher": "Bash", "hooks": [{"type": "command", "command": "/bin/pre", "timeout": 5}]}
			],
			"PostToolUse": [
				{"matcher": "Bash", "hooks": [{"type": "command", "command": "/bin/post", "timeout": 3}]}
			]
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	hooks := readGeminiHooks(t, filepath.Join(geminiHome, "settings.json"))
	if _, exists := hooks["PreToolUse"]; exists {
		t.Fatalf("gemini emit should rewrite PreToolUse to BeforeTool; got %v", hooks)
	}
	if _, exists := hooks["PostToolUse"]; exists {
		t.Fatalf("gemini emit should rewrite PostToolUse to AfterTool; got %v", hooks)
	}
	beforeSpec := hooks["BeforeTool"][0]["hooks"].([]any)[0].(map[string]any)
	afterSpec := hooks["AfterTool"][0]["hooks"].([]any)[0].(map[string]any)
	if beforeSpec["timeout"] != float64(5000) {
		t.Errorf("BeforeTool timeout = %v; want 5000 ms", beforeSpec["timeout"])
	}
	if afterSpec["timeout"] != float64(3000) {
		t.Errorf("AfterTool timeout = %v; want 3000 ms", afterSpec["timeout"])
	}
	if beforeSpec["name"] == "" || afterSpec["name"] == "" || beforeSpec["name"] == afterSpec["name"] {
		t.Errorf("multi-spec synthesized names should be non-empty and distinct; got %v / %v", beforeSpec["name"], afterSpec["name"])
	}
}

func TestInstall_HookOnGeminiCLI_PreservesGeminiExtensions(t *testing.T) {
	geminiHome, _, _ := setupGeminiHookEnv(t)

	src := filepath.Join(t.TempDir(), "gemini-rich.hook.json")
	if err := os.WriteFile(src, []byte(`{
		"hooks": {
			"PreToolUse": [
				{
					"matcher": "Bash",
					"hooks": [
						{
							"type": "command",
							"command": "/bin/rich",
							"name": "rich-hook",
							"description": "friendly label",
							"async": true,
							"once": true
						}
					]
				}
			]
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	hooks := readGeminiHooks(t, filepath.Join(geminiHome, "settings.json"))
	spec := hooks["BeforeTool"][0]["hooks"].([]any)[0].(map[string]any)
	if spec["name"] != "rich-hook" {
		t.Errorf("name extension should pass through on gemini-cli; got %v", spec["name"])
	}
	if spec["description"] != "friendly label" {
		t.Errorf("description extension should pass through on gemini-cli; got %v", spec["description"])
	}
	if spec["async"] != true {
		t.Errorf("async extension should pass through on gemini-cli; got %v", spec["async"])
	}
	if spec["once"] != true {
		t.Errorf("once extension should pass through on gemini-cli; got %v", spec["once"])
	}
}

func TestInstall_HookOnGeminiCLI_SiblingKeyPreservation_AcrossInstallUninstall(t *testing.T) {
	geminiHome, projectHome, dotpackHome := setupGeminiHookEnv(t)
	settingsPath := filepath.Join(geminiHome, "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{
  "theme": "dark",
  "hooksConfig": {"notifications": false},
  "hooks": {
    "BeforeTool": [
      {
        "matcher": "read_file",
        "hooks": [{"type": "command", "command": "/usr/local/bin/read-guard.sh", "name": "read-guard"}]
      }
    ],
    "SessionStart": [
      {
        "hooks": [{"type": "command", "command": "/usr/local/bin/welcome.sh", "name": "welcome"}]
      }
    ]
  }
}
`)
	if err := os.WriteFile(settingsPath, existing, 0o600); err != nil {
		t.Fatal(err)
	}

	got := geminiHookInstallHelper(t, geminiHome, projectHome, dotpackHome, "bash-guard.hook.json", "user")
	if got != settingsPath {
		t.Fatalf("unexpected settings path: %s", got)
	}
	geminiHookUninstall(t, "gemini-cli:hook:bash-guard")

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse settings: %v\n%s", err, raw)
	}
	if root["theme"] != "dark" {
		t.Errorf("top-level sibling key lost; got %v", root["theme"])
	}
	hooks := readGeminiHooks(t, settingsPath)
	if len(hooks["BeforeTool"]) != 1 || hooks["BeforeTool"][0]["matcher"] != "read_file" {
		t.Errorf("user-authored BeforeTool sibling lost; got %v", hooks["BeforeTool"])
	}
	if len(hooks["SessionStart"]) != 1 {
		t.Errorf("SessionStart sibling lost; got %v", hooks["SessionStart"])
	}
	if cfg, _ := root["hooksConfig"].(map[string]any); cfg == nil || cfg["notifications"] != false {
		t.Errorf("hooksConfig sibling mutated; got %v", root["hooksConfig"])
	}
	if st, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("stat settings: %v", err)
	} else if st.Mode().Perm() != 0o600 {
		t.Errorf("settings mode = %#o; want 0600", st.Mode().Perm())
	}
}

func TestInstall_HookOnGeminiCLI_SymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	geminiHome, _, _ := setupGeminiHookEnv(t)
	settingsPath := filepath.Join(geminiHome, "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, settingsPath); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--kind", "hook", "--scope", "user"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected symlink collision refusal, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should name symlink refusal; got %v", err)
	}
}
