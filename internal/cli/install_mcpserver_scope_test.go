package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestInstall_MCPServerOnClaudeCode_UserScope_FreshFile(t *testing.T) {
	homeDir := t.TempDir()
	claudeHome := filepath.Join(homeDir, ".claude")
	t.Setenv("DOTPACK_USER_HOME", homeDir)
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install claude user-scope mcp-server: %v", err)
	}

	configPath := filepath.Join(homeDir, ".claude.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read ~/.claude.json: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse ~/.claude.json: %v\n%s", err, raw)
	}
	entry := root["mcpServers"].(map[string]any)["github"].(map[string]any)
	if entry["command"] != "npx" {
		t.Errorf("mcpServers.github.command = %v; want npx", entry["command"])
	}
}

func setupCodexProjectConfigEnv(t *testing.T) (projectHome, dotpackHome string) {
	t.Helper()
	projectHome = t.TempDir()
	dotpackHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	return projectHome, dotpackHome
}

func TestInstall_MCPServerOnCodex_ProjectScope_FreshFile(t *testing.T) {
	projectHome, _ := setupCodexProjectConfigEnv(t)

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "mcp-server", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install codex project-scope mcp-server: %v", err)
	}

	configPath := filepath.Join(projectHome, ".codex", "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read project config.toml: %v", err)
	}
	var root map[string]any
	if err := toml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse project config.toml: %v\n%s", err, raw)
	}
	entry := root["mcp_servers"].(map[string]any)["github"].(map[string]any)
	if entry["command"] != "npx" {
		t.Errorf("mcp_servers.github.command = %v; want npx", entry["command"])
	}
}

func TestInstall_HookOnCodex_ProjectScope_FreshFile(t *testing.T) {
	projectHome, _ := setupCodexProjectConfigEnv(t)

	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "hook", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install codex project-scope hook: %v", err)
	}

	configPath := filepath.Join(projectHome, ".codex", "config.toml")
	hooks := readCodexHooks(t, configPath)
	if len(hooks["PreToolUse"]) != 1 {
		t.Fatalf("expected one PreToolUse binding in project config; got %v", hooks)
	}
}
