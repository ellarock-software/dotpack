package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setupOpenCodeEnv points dotpack at temp homes for an opencode install.
func setupOpenCodeEnv(t *testing.T) (openCodeHome, dotpackHome, projectHome string) {
	t.Helper()
	openCodeHome = t.TempDir()
	dotpackHome = t.TempDir()
	projectHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_ANTIGRAVITY_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", t.TempDir())
	t.Setenv("DOTPACK_OPENCODE_HOME", openCodeHome)
	t.Setenv("DOTPACK_CODEX_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", projectHome)
	return openCodeHome, dotpackHome, projectHome
}

// TestInstall_OpenCode_SkillAndMCP_EndToEnd proves the newly onboarded
// OpenCode adapter works through the real CLI: a skill drops a SKILL.md,
// an mcp-server merges into opencode.json under $.mcp, and uninstall
// cleanly reverses the merge.
func TestInstall_OpenCode_SkillAndMCP_EndToEnd(t *testing.T) {
	openCodeHome, _, _ := setupOpenCodeEnv(t)
	tmp := t.TempDir()

	skillSrc := filepath.Join(tmp, "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillSrc), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(skillSrc, []byte("---\nname: demo\ndescription: a demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	runDotpack(t, "install", skillSrc, "--agent", "opencode", "--scope", "user")
	if _, err := os.Stat(filepath.Join(openCodeHome, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatalf("expected opencode SKILL.md: %v", err)
	}

	mcpSrc := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	runDotpack(t, "install", mcpSrc, "--agent", "opencode", "--kind", "mcp-server", "--scope", "user")

	configPath := filepath.Join(openCodeHome, "opencode.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse opencode.json: %v\n%s", err, raw)
	}
	mcp, _ := cfg["mcp"].(map[string]any)
	gh, ok := mcp["github"].(map[string]any)
	if !ok || gh["type"] != "local" {
		t.Fatalf("opencode.json $.mcp.github = %#v; want local server", cfg["mcp"])
	}

	// uninstall reverses the merge (leaf removed; file retained).
	runDotpack(t, "uninstall", "opencode:mcp-server:github")
	raw, _ = os.ReadFile(configPath)
	_ = json.Unmarshal(raw, &cfg)
	if mcp, _ := cfg["mcp"].(map[string]any); len(mcp) != 0 {
		t.Fatalf("opencode mcp leaf survived uninstall: %#v", cfg["mcp"])
	}
}
