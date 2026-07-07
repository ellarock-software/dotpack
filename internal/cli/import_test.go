package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportClaudeCodeProjectWritesAgentsTree(t *testing.T) {
	srcProject := t.TempDir()
	claude := filepath.Join(srcProject, ".claude")
	mustWrite(t, filepath.Join(claude, "skills", "demo", "SKILL.md"), "# Demo\n\nRun `.claude/hooks/demo.mjs`.\n")
	mustWrite(t, filepath.Join(claude, "hooks", "demo.mjs"), "console.log('.claude/hooks/demo.mjs')\n")
	mustWrite(t, filepath.Join(claude, "logs", "ignored.log"), "runtime\n")
	mustWrite(t, filepath.Join(claude, "judge", "state", "active.json"), "{}\n")
	mustWrite(t, filepath.Join(claude, "scratch.local.json"), "{}\n")
	mustWrite(t, filepath.Join(claude, "settings.json"), `{
  "env": { "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1" },
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "node ${CLAUDE_PROJECT_DIR}/.claude/hooks/demo.mjs" }] }
    ]
  }
}
`)

	outProject := t.TempDir()
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"import", "claude-code", srcProject, "--out", outProject})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import: %v\n%s", err, stdout.String())
	}

	skill := readFile(t, filepath.Join(outProject, ".agents", "skills", "demo", "SKILL.md"))
	if strings.Contains(skill, ".claude/") || !strings.Contains(skill, ".agents/hooks/demo.mjs") {
		t.Fatalf("skill path refs not rewritten: %q", skill)
	}

	registry := readFile(t, filepath.Join(outProject, ".agents", "hooks", "registry.json"))
	if strings.Contains(registry, ".claude/") || !strings.Contains(registry, ".agents/hooks/demo.mjs") {
		t.Fatalf("hook registry not imported/re-written: %q", registry)
	}

	settings := readFile(t, filepath.Join(outProject, ".agents", "config", "claude-code.settings.json"))
	if strings.Contains(settings, ".claude/") || !strings.Contains(settings, "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS") {
		t.Fatalf("settings not preserved/re-written: %q", settings)
	}

	if _, err := os.Stat(filepath.Join(outProject, ".agents", "logs", "ignored.log")); !os.IsNotExist(err) {
		t.Fatalf("runtime logs should not be copied, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outProject, ".agents", "judge", "state", "active.json")); !os.IsNotExist(err) {
		t.Fatalf("judge runtime state should not be copied, stat err = %v", err)
	}
}

func TestImportClaudeCodeRefusesOverwriteWithoutForce(t *testing.T) {
	srcProject := t.TempDir()
	mustWrite(t, filepath.Join(srcProject, ".claude", "skills", "demo", "SKILL.md"), "# Demo\n")

	outProject := t.TempDir()
	target := filepath.Join(outProject, ".agents", "skills", "demo", "SKILL.md")
	mustWrite(t, target, "# Existing\n")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"import", "claude-code", srcProject, "--out", outProject})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "pass --force") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}

	forced := NewRootCmd()
	forced.SetOut(io_DiscardWriter())
	forced.SetErr(io_DiscardWriter())
	forced.SetArgs([]string{"import", "claude-code", srcProject, "--out", outProject, "--force"})
	if err := forced.Execute(); err != nil {
		t.Fatalf("import --force: %v", err)
	}
	if got := readFile(t, target); got != "# Demo\n" {
		t.Fatalf("force did not overwrite target: %q", got)
	}
}

func TestImportRejectsUnsupportedSourceAgent(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"import", "codex", t.TempDir()})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported-agent error, got %v", err)
	}
}

func mustWrite(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
