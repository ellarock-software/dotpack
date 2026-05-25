package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func setupGeminiMCPEnv(t *testing.T) (geminiHome, projectHome, dotpackHome string) {
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

func TestInstall_MCPServerOnGeminiCLI_UserScope_FreshFile(t *testing.T) {
	geminiHome, _, dotpackHome := setupGeminiMCPEnv(t)

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "gemini-cli",
		"--kind", "mcp-server",
		"--scope", "user",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if !strings.Contains(got, "Installed gemini-cli:mcp-server:github") {
		t.Errorf("expected success message naming the install ID; got %q", got)
	}

	settingsPath := filepath.Join(geminiHome, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse settings.json: %v\nfile content:\n%s", err, raw)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if servers == nil {
		t.Fatalf("expected mcpServers map at root; got %s", raw)
	}
	entry, _ := servers["github"].(map[string]any)
	if entry == nil {
		t.Fatalf("expected mcpServers.github entry; got %s", raw)
	}
	if got := entry["command"]; got != "npx" {
		t.Errorf("mcpServers.github.command = %v; want npx", got)
	}
	args, _ := entry["args"].([]any)
	if len(args) != 2 || args[0] != "-y" || args[1] != "@modelcontextprotocol/server-github" {
		t.Errorf("mcpServers.github.args = %v; want [-y, @modelcontextprotocol/server-github]", args)
	}
	env, _ := entry["env"].(map[string]any)
	if env == nil || env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "${GITHUB_TOKEN}" {
		t.Errorf("mcpServers.github.env should pass through verbatim; got %v", env)
	}

	type mkPersisted struct {
		File string `yaml:"file"`
		Path string `yaml:"path"`
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
	if rec.ID != "gemini-cli:mcp-server:github" {
		t.Errorf("record ID = %q; want gemini-cli:mcp-server:github", rec.ID)
	}
	if rec.Kind != "mcp-server" {
		t.Errorf("record Kind = %q; want mcp-server", rec.Kind)
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
	if rec.MergedKeys[0].File != settingsPath {
		t.Errorf("merged_keys[0].file = %q; want %q", rec.MergedKeys[0].File, settingsPath)
	}
	if rec.MergedKeys[0].Path != "$.mcpServers.github" {
		t.Errorf("merged_keys[0].path = %q; want $.mcpServers.github", rec.MergedKeys[0].Path)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "gemini-cli:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if raw2, err := os.ReadFile(settingsPath); err == nil {
		var parsed2 map[string]any
		if jerr := json.Unmarshal(raw2, &parsed2); jerr == nil {
			if servers2, ok := parsed2["mcpServers"].(map[string]any); ok {
				if _, exists := servers2["github"]; exists {
					t.Errorf("expected $.mcpServers.github removed after uninstall; got %s", raw2)
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

func TestInstall_MCPServerOnGeminiCLI_ProjectScope_FreshFile(t *testing.T) {
	_, projectHome, _ := setupGeminiMCPEnv(t)

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "project",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	settingsPath := filepath.Join(projectHome, ".gemini", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read project settings.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse project settings.json: %v\n%s", err, raw)
	}
	servers := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["github"].(map[string]any); !ok {
		t.Fatalf("expected $.mcpServers.github in project settings; got %s", raw)
	}
}

func TestInstall_MCPServerOnGeminiCLI_PreservesGeminiExtensions(t *testing.T) {
	geminiHome, _, _ := setupGeminiMCPEnv(t)

	src := filepath.Join(t.TempDir(), "gemini-rich.mcp.json")
	if err := os.WriteFile(src, []byte(`{
		"mcpServers": {
			"gemini-rich": {
				"command": "node",
				"args": ["server.js"],
				"cwd": "/tmp/project",
				"timeout": 45,
				"trust": true,
				"type": "stdio"
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "user",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(geminiHome, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse settings: %v\n%s", err, raw)
	}
	entry := parsed["mcpServers"].(map[string]any)["gemini-rich"].(map[string]any)
	if entry["cwd"] != "/tmp/project" {
		t.Errorf("cwd must pass through on gemini-cli; got %v", entry["cwd"])
	}
	if entry["timeout"] != float64(45) {
		t.Errorf("timeout must pass through on gemini-cli; got %v", entry["timeout"])
	}
	if entry["trust"] != true {
		t.Errorf("trust must pass through on gemini-cli; got %v", entry["trust"])
	}
	if entry["type"] != "stdio" {
		t.Errorf("non-lossy type metadata must pass through; got %v", entry["type"])
	}
}

func TestInstall_MCPServerOnGeminiCLI_CodexOnlyExtensionLossyRefused(t *testing.T) {
	setupGeminiMCPEnv(t)

	src := filepath.Join(t.TempDir(), "codex-rich.mcp.json")
	if err := os.WriteFile(src, []byte(`{
		"mcpServers": {
			"codex-rich": {
				"command": "node",
				"args": ["server.js"],
				"enabled_tools": ["read"]
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "user",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected LossyError for codex-only extension on gemini-cli, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"enabled_tools", "codex_tool_allowlist", "--allow-lossy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("lossy error missing %q; got:\n%s", want, msg)
		}
	}
}

func TestInstall_MCPServerOnGeminiCLI_PreExistingFile_SiblingsPreserved(t *testing.T) {
	geminiHome, _, _ := setupGeminiMCPEnv(t)
	settingsPath := filepath.Join(geminiHome, "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings parent: %v", err)
	}
	before := []byte(`{
  "theme": "dark",
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    }
  }
}
`)
	if err := os.WriteFile(settingsPath, before, 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "user",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "gemini-cli:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after uninstall: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse settings after uninstall: %v\n%s", err, raw)
	}
	if parsed["theme"] != "dark" {
		t.Errorf("top-level sibling key lost; got %v", parsed["theme"])
	}
	servers := parsed["mcpServers"].(map[string]any)
	if _, ok := servers["filesystem"].(map[string]any); !ok {
		t.Errorf("sibling mcp server lost after install/uninstall; got %s", raw)
	}
	if _, exists := servers["github"]; exists {
		t.Errorf("installed mcp server should be removed after uninstall; got %s", raw)
	}
	if st, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("stat settings: %v", err)
	} else if st.Mode().Perm() != 0o600 {
		t.Errorf("settings mode = %#o; want 0600", st.Mode().Perm())
	}
}

func TestInstall_MCPServerOnGeminiCLI_SymlinkTargetRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	geminiHome, _, _ := setupGeminiMCPEnv(t)
	settingsPath := filepath.Join(geminiHome, "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings parent: %v", err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(target, settingsPath); err != nil {
		t.Fatalf("symlink settings: %v", err)
	}

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", src,
		"--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "user",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected symlink collision refusal, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should name symlink refusal; got %v", err)
	}
}

func TestInstall_MCPServerOnGeminiCLI_ReinstallReplaces(t *testing.T) {
	geminiHome, _, _ := setupGeminiMCPEnv(t)

	srcV1 := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install", srcV1,
		"--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "user",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install v1: %v", err)
	}

	srcV2 := filepath.Join(t.TempDir(), "github-v2.mcp.json")
	if err := os.WriteFile(srcV2, []byte(`{
		"mcpServers": {
			"github": {
				"command": "uvx",
				"args": ["mcp-github"]
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	reinstall := NewRootCmd()
	reinstall.SetOut(io_DiscardWriter())
	reinstall.SetErr(io_DiscardWriter())
	reinstall.SetArgs([]string{
		"install", srcV2,
		"--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "user",
	})
	if err := reinstall.Execute(); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(geminiHome, "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse settings: %v\n%s", err, raw)
	}
	entry := parsed["mcpServers"].(map[string]any)["github"].(map[string]any)
	if entry["command"] != "uvx" {
		t.Errorf("reinstall should replace command; got %v", entry["command"])
	}
	if _, exists := entry["env"]; exists {
		t.Errorf("reinstall should replace stale env from v1; got %v", entry["env"])
	}
}
