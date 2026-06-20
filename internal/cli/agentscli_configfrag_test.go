package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

func TestInstall_MCPServerOnAgentsCli_UserScope_FansOutToGeminiAndCodex(t *testing.T) {
	_, dotpackHome := setupAgentsCliEnv(t)
	geminiHome := os.Getenv("DOTPACK_GEMINI_HOME")
	antigravityHome := os.Getenv("DOTPACK_ANTIGRAVITY_HOME")
	codexHome := os.Getenv("DOTPACK_CODEX_HOME")

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--kind", "mcp-server", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install agents-cli mcp-server: %v", err)
	}

	geminiPath := filepath.Join(geminiHome, "settings.json")
	geminiRaw, err := os.ReadFile(geminiPath)
	if err != nil {
		t.Fatalf("read gemini settings: %v", err)
	}
	var geminiRoot map[string]any
	if err := json.Unmarshal(geminiRaw, &geminiRoot); err != nil {
		t.Fatalf("parse gemini settings: %v\n%s", err, geminiRaw)
	}
	geminiEntry := geminiRoot["mcpServers"].(map[string]any)["github"].(map[string]any)
	if geminiEntry["command"] != "npx" {
		t.Errorf("gemini mcp command = %v; want npx", geminiEntry["command"])
	}

	antigravityPath := filepath.Join(antigravityHome, "settings.json")
	antigravityRaw, err := os.ReadFile(antigravityPath)
	if err != nil {
		t.Fatalf("read antigravity settings: %v", err)
	}
	var antigravityRoot map[string]any
	if err := json.Unmarshal(antigravityRaw, &antigravityRoot); err != nil {
		t.Fatalf("parse antigravity settings: %v\n%s", err, antigravityRaw)
	}
	antigravityEntry := antigravityRoot["mcpServers"].(map[string]any)["github"].(map[string]any)
	if antigravityEntry["command"] != "npx" {
		t.Errorf("antigravity mcp command = %v; want npx", antigravityEntry["command"])
	}

	codexPath := filepath.Join(codexHome, "config.toml")
	codexRaw, err := os.ReadFile(codexPath)
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	var codexRoot map[string]any
	if err := toml.Unmarshal(codexRaw, &codexRoot); err != nil {
		t.Fatalf("parse codex config: %v\n%s", err, codexRaw)
	}
	codexEntry := codexRoot["mcp_servers"].(map[string]any)["github"].(map[string]any)
	if codexEntry["command"] != "npx" {
		t.Errorf("codex mcp command = %v; want npx", codexEntry["command"])
	}

	var manifestRaw struct {
		Installs []struct {
			ID         string `yaml:"id"`
			Agent      string `yaml:"agent"`
			Kind       string `yaml:"kind"`
			MergedKeys []struct {
				File string `yaml:"file"`
				Path string `yaml:"path"`
			} `yaml:"merged_keys,omitempty"`
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
		t.Fatalf("expected one umbrella manifest record; got %d:\n%s", len(manifestRaw.Installs), mr)
	}
	rec := manifestRaw.Installs[0]
	if rec.ID != "agents-cli:mcp-server:github" || rec.Agent != "agents-cli" || rec.Kind != "mcp-server" {
		t.Fatalf("wrong manifest record identity: %+v", rec)
	}
	if len(rec.MergedKeys) != 3 {
		t.Fatalf("expected three merged_keys entries; got %d (%v)", len(rec.MergedKeys), rec.MergedKeys)
	}
	seen := map[string]bool{}
	for _, mk := range rec.MergedKeys {
		seen[mk.File+"#"+mk.Path] = true
	}
	if !seen[geminiPath+"#$.mcpServers.github"] {
		t.Errorf("manifest missing gemini merged key; got %+v", rec.MergedKeys)
	}
	if !seen[antigravityPath+"#$.mcpServers.github"] {
		t.Errorf("manifest missing antigravity merged key; got %+v", rec.MergedKeys)
	}
	if !seen[codexPath+"#mcp_servers.github"] {
		t.Errorf("manifest missing codex merged key; got %+v", rec.MergedKeys)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "agents-cli:mcp-server:github"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall umbrella mcp-server: %v", err)
	}
	if raw, err := os.ReadFile(geminiPath); err == nil {
		var root map[string]any
		_ = json.Unmarshal(raw, &root)
		if servers, ok := root["mcpServers"].(map[string]any); ok {
			if _, exists := servers["github"]; exists {
				t.Errorf("gemini mcp entry survived uninstall; got %s", raw)
			}
		}
	}
	if raw, err := os.ReadFile(antigravityPath); err == nil {
		var root map[string]any
		_ = json.Unmarshal(raw, &root)
		if servers, ok := root["mcpServers"].(map[string]any); ok {
			if _, exists := servers["github"]; exists {
				t.Errorf("antigravity mcp entry survived uninstall; got %s", raw)
			}
		}
	}
	if raw, err := os.ReadFile(codexPath); err == nil {
		var root map[string]any
		_ = toml.Unmarshal(raw, &root)
		if servers, ok := root["mcp_servers"].(map[string]any); ok {
			if _, exists := servers["github"]; exists {
				t.Errorf("codex mcp entry survived uninstall; got %s", raw)
			}
		}
	}
}

func TestInstall_HookOnAgentsCli_UserScope_FansOutToGeminiAndCodex(t *testing.T) {
	_, dotpackHome := setupAgentsCliEnv(t)
	geminiPath := filepath.Join(os.Getenv("DOTPACK_GEMINI_HOME"), "settings.json")
	antigravityPath := filepath.Join(os.Getenv("DOTPACK_ANTIGRAVITY_HOME"), "settings.json")
	codexPath := filepath.Join(os.Getenv("DOTPACK_CODEX_HOME"), "config.toml")

	src := filepath.Join("..", "resource", "testdata", "hooks", "bash-guard.hook.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install agents-cli hook: %v", err)
	}

	geminiHooks := readGeminiHooks(t, geminiPath)
	if len(geminiHooks["BeforeTool"]) != 1 {
		t.Fatalf("expected one Gemini BeforeTool binding; got %v", geminiHooks)
	}
	if _, exists := geminiHooks["PreToolUse"]; exists {
		t.Fatalf("Gemini hook should use BeforeTool alias, not PreToolUse: %v", geminiHooks)
	}
	antigravityHooks := readGeminiHooks(t, antigravityPath)
	if len(antigravityHooks["BeforeTool"]) != 1 {
		t.Fatalf("expected one Antigravity BeforeTool binding; got %v", antigravityHooks)
	}
	if _, exists := antigravityHooks["PreToolUse"]; exists {
		t.Fatalf("Antigravity hook should use BeforeTool alias, not PreToolUse: %v", antigravityHooks)
	}
	codexHooks := readCodexHooks(t, codexPath)
	if len(codexHooks["PreToolUse"]) != 1 {
		t.Fatalf("expected one Codex PreToolUse binding; got %v", codexHooks)
	}

	var manifestRaw struct {
		Installs []struct {
			ID         string `yaml:"id"`
			Agent      string `yaml:"agent"`
			Kind       string `yaml:"kind"`
			MergedKeys []struct {
				File     string `yaml:"file"`
				Path     string `yaml:"path"`
				Op       string `yaml:"op,omitempty"`
				Selector string `yaml:"selector,omitempty"`
			} `yaml:"merged_keys,omitempty"`
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
		t.Fatalf("expected one umbrella manifest record; got %d:\n%s", len(manifestRaw.Installs), mr)
	}
	rec := manifestRaw.Installs[0]
	if rec.ID != "agents-cli:hook:bash-guard" || rec.Agent != "agents-cli" || rec.Kind != "hook" {
		t.Fatalf("wrong manifest record identity: %+v", rec)
	}
	if len(rec.MergedKeys) != 3 {
		t.Fatalf("expected three merged_keys entries; got %d (%v)", len(rec.MergedKeys), rec.MergedKeys)
	}
	seen := map[string]bool{}
	for _, mk := range rec.MergedKeys {
		if mk.Op != "append" || !strings.HasPrefix(mk.Selector, "sha256:") {
			t.Errorf("hook merged key must carry append op and selector; got %+v", mk)
		}
		seen[mk.File+"#"+mk.Path] = true
	}
	if !seen[geminiPath+"#$.hooks.BeforeTool"] {
		t.Errorf("manifest missing gemini hook merged key; got %+v", rec.MergedKeys)
	}
	if !seen[antigravityPath+"#$.hooks.BeforeTool"] {
		t.Errorf("manifest missing antigravity hook merged key; got %+v", rec.MergedKeys)
	}
	if !seen[codexPath+"#hooks.PreToolUse"] {
		t.Errorf("manifest missing codex hook merged key; got %+v", rec.MergedKeys)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "agents-cli:hook:bash-guard"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall umbrella hook: %v", err)
	}
	if hooks := readGeminiHooks(t, geminiPath); len(hooks["BeforeTool"]) != 0 {
		t.Errorf("gemini hook survived uninstall; got %v", hooks["BeforeTool"])
	}
	if hooks := readGeminiHooks(t, antigravityPath); len(hooks["BeforeTool"]) != 0 {
		t.Errorf("antigravity hook survived uninstall; got %v", hooks["BeforeTool"])
	}
	if hooks := readCodexHooks(t, codexPath); len(hooks["PreToolUse"]) != 0 {
		t.Errorf("codex hook survived uninstall; got %v", hooks["PreToolUse"])
	}
}

func TestInstall_HookOnAgentsCli_RewritesMonitoredClaudeProjectDirForPortableHosts(t *testing.T) {
	setupAgentsCliEnv(t)
	geminiPath := filepath.Join(os.Getenv("DOTPACK_GEMINI_HOME"), "settings.json")
	antigravityPath := filepath.Join(os.Getenv("DOTPACK_ANTIGRAVITY_HOME"), "settings.json")
	codexPath := filepath.Join(os.Getenv("DOTPACK_CODEX_HOME"), "config.toml")

	src := filepath.Join("..", "resource", "testdata", "hooks", "observed-bash-guard.hook.json")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--kind", "hook", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install agents-cli observed hook: %v", err)
	}

	want := "bash \"$(git rev-parse --show-toplevel)/.agents/hooks/hook-monitor.sh\" \"$(git rev-parse --show-toplevel)/.agents/hooks/bash-guard.sh\""

	geminiHooks := readGeminiHooks(t, geminiPath)
	geminiCommand := geminiHooks["BeforeTool"][0]["hooks"].([]any)[0].(map[string]any)["command"]
	if geminiCommand != want {
		t.Fatalf("gemini monitored command = %q; want %q", geminiCommand, want)
	}

	antigravityHooks := readGeminiHooks(t, antigravityPath)
	antigravityCommand := antigravityHooks["BeforeTool"][0]["hooks"].([]any)[0].(map[string]any)["command"]
	if antigravityCommand != want {
		t.Fatalf("antigravity monitored command = %q; want %q", antigravityCommand, want)
	}

	codexHooks := readCodexHooks(t, codexPath)
	codexCommand := codexHooks["PreToolUse"][0]["hooks"].([]any)[0].(map[string]any)["command"]
	if codexCommand != want {
		t.Fatalf("codex monitored command = %q; want %q", codexCommand, want)
	}
}

func TestInstall_MCPServerOnAgentsCli_CodexOnlyExtensionLossyRefused(t *testing.T) {
	setupAgentsCliEnv(t)

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
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--kind", "mcp-server", "--scope", "user"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected agents-cli lossy refusal for codex-only extension, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"agents-cli", "enabled_tools", "codex_tool_allowlist", "--allow-lossy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("lossy error missing %q; got:\n%s", want, msg)
		}
	}
}
