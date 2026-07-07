package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func setupHermesEnv(t *testing.T) (hermesHome, dotpackHome, projectHome string) {
	t.Helper()
	hermesHome = t.TempDir()
	dotpackHome = t.TempDir()
	projectHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_ANTIGRAVITY_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_CODEX_HOME", t.TempDir())
	t.Setenv("DOTPACK_OPENCODE_HOME", t.TempDir())
	t.Setenv("DOTPACK_HERMES_HOME", hermesHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	return hermesHome, dotpackHome, projectHome
}

func TestInstall_Hermes_SkillAndMemory_EndToEnd(t *testing.T) {
	hermesHome, _, projectHome := setupHermesEnv(t)
	tmp := t.TempDir()

	skillSrc := filepath.Join(tmp, "demo", "SKILL.md")
	mustWriteTestFile(t, skillSrc, "---\nname: demo\ndescription: a demo\nversion: 1.2.3\nauthor: Hermes\nplatforms:\n  - macos\n---\nbody\n")
	runDotpack(t, "install", skillSrc, "--agent", "hermes", "--scope", "user")
	if _, err := os.Stat(filepath.Join(hermesHome, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatalf("expected Hermes skill install: %v", err)
	}

	memorySrc := filepath.Join(tmp, "GEMINI.md")
	mustWriteTestFile(t, memorySrc, "project memory\n")
	runDotpack(t, "install", memorySrc, "--agent", "hermes", "--kind", "memory", "--scope", "project")
	raw, err := os.ReadFile(filepath.Join(projectHome, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read Hermes project memory: %v", err)
	}
	if string(raw) != "project memory\n" {
		t.Fatalf("project memory = %q; want source bytes preserved", string(raw))
	}

	soulSrc := filepath.Join(tmp, "SOUL.md")
	mustWriteTestFile(t, soulSrc, "global identity\n")
	runDotpack(t, "install", soulSrc, "--agent", "hermes", "--kind", "memory", "--scope", "user")
	raw, err = os.ReadFile(filepath.Join(hermesHome, "SOUL.md"))
	if err != nil {
		t.Fatalf("read Hermes SOUL.md: %v", err)
	}
	if string(raw) != "global identity\n" {
		t.Fatalf("SOUL.md = %q; want source bytes preserved", string(raw))
	}
}

func TestInstall_Hermes_MCPAndHook_EndToEnd(t *testing.T) {
	hermesHome, _, _ := setupHermesEnv(t)
	tmp := t.TempDir()

	mcpSrc := filepath.Join(tmp, "github.mcp.json")
	mustWriteTestFile(t, mcpSrc, `{"mcpServers":{"github":{"command":"npx","args":["-y","@modelcontextprotocol/server-github"],"env":{"GITHUB_TOKEN":"x"},"enabled":true,"timeout":45,"connect_timeout":15,"supports_parallel_tool_calls":true,"headers":{"Authorization":"Bearer x"},"auth":"oauth","sampling":{"enabled":false},"ssl_verify":false,"client_cert":"/tmp/client.pem","client_key":"/tmp/client.key","tools":{"include":["list_issues"],"resources":false,"prompts":false}}}}`)
	runDotpack(t, "install", mcpSrc, "--agent", "hermes", "--kind", "mcp-server", "--scope", "user")

	hookSrc := filepath.Join(tmp, "guard.hook.json")
	mustWriteTestFile(t, hookSrc, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"python hook.py --root $CLAUDE_PROJECT_DIR && echo ok","timeout":8,"env":{"OACB_TIER":"gold"}}]}]}}`)
	runDotpack(t, "install", hookSrc, "--agent", "hermes", "--kind", "hook", "--scope", "user")

	cfgPath := filepath.Join(hermesHome, "config.yaml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read Hermes config.yaml: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.yaml: %v\n%s", err, raw)
	}

	mcp := cfg["mcp_servers"].(map[string]any)["github"].(map[string]any)
	for _, key := range []string{"enabled", "timeout", "connect_timeout", "supports_parallel_tool_calls", "headers", "auth", "sampling", "ssl_verify", "client_cert", "client_key", "tools"} {
		if _, ok := mcp[key]; !ok {
			t.Fatalf("mcp config missing %q: %#v", key, mcp)
		}
	}

	hooks := cfg["hooks"].(map[string]any)
	preTool := hooks["pre_tool_call"].([]any)
	if len(preTool) != 1 {
		t.Fatalf("pre_tool_call hooks = %#v; want 1 entry", preTool)
	}
	entry := preTool[0].(map[string]any)
	command := entry["command"].(string)
	for _, want := range []string{"env OACB_TIER='gold'", "bash -lc", "$(git rev-parse --show-toplevel)"} {
		if !strings.Contains(command, want) {
			t.Fatalf("hook command = %q; want %q", command, want)
		}
	}
	if entry["matcher"] != "Bash" {
		t.Fatalf("hook entry = %#v; want matcher Bash", entry)
	}

	runDotpack(t, "uninstall", "hermes:mcp-server:github")
	runDotpack(t, "uninstall", "hermes:hook:guard")

	raw, err = os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.yaml after uninstall: %v", err)
	}
	cfg = nil
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse config.yaml after uninstall: %v\n%s", err, raw)
	}
	if servers, _ := cfg["mcp_servers"].(map[string]any); len(servers) != 0 {
		t.Fatalf("mcp server survived uninstall: %#v", servers)
	}
	if hooks, _ := cfg["hooks"].(map[string]any); len(hooks) != 0 {
		if event, ok := hooks["pre_tool_call"].([]any); !ok || len(event) != 0 || len(hooks) != 1 {
			t.Fatalf("hook survived uninstall: %#v", hooks)
		}
	}
}
