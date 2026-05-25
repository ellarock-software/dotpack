package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupGeminiEnv installs DOTPACK_* env vars pointing at fresh tempdirs
// for each major root. ProjectHome is set too so project-scope branches
// have a valid target if exercised.
func setupGeminiEnv(t *testing.T) (geminiHome, dotpackHome string) {
	t.Helper()
	geminiHome = t.TempDir()
	dotpackHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	return geminiHome, dotpackHome
}

func TestInstall_Skill_OnGeminiCLI_EndToEnd(t *testing.T) {
	// Smoke test: the tracer-bullet skill (universal-core only) installs
	// onto gemini-cli without --allow-lossy. Confirms the CLI is wired
	// (--agent gemini-cli accepted) and the gemini adapter's plan/apply
	// path produces the expected on-disk layout.
	geminiHome, _ := setupGeminiEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--scope", "user"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "Installed gemini-cli:skill:dotpack-tracer-bullet") {
		t.Errorf("expected success message; got %q", got)
	}
	target := filepath.Join(geminiHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected SKILL.md at %s: %v", target, err)
	}
}

func TestInstall_SkillWithAllowedTools_OnGeminiCLI_LossyRefused(t *testing.T) {
	// DISCRIMINATING TEST 1 (per advisor): first real-host §8 firing.
	// A skill carrying `allowed-tools` (bound to claude_skill_runtime_overrides,
	// supported only on claude-code) → install --agent gemini-cli with
	// no --allow-lossy must refuse with LossyError. Pre-#7 every §8 test
	// of a real non-claude host used a synthetic HostID string; this is
	// the first test where a real adapter's HostID drives the lossy gate.
	setupGeminiEnv(t)

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "claudish-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(skillDir, "SKILL.md")
	body := []byte(`---
name: claudish-skill
description: skill with claude-only allowed-tools
allowed-tools: Read, Write, Edit
---
body content
`)
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected LossyError for allowed-tools on gemini-cli, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "gemini-cli") {
		t.Errorf("error must name the lossy host; got %q", msg)
	}
	if !strings.Contains(msg, "allowed-tools") {
		t.Errorf("error must name the lossy field; got %q", msg)
	}
	if !strings.Contains(msg, "claude_skill_runtime_overrides") {
		t.Errorf("error must name the canonical concept (so user sees WHY); got %q", msg)
	}
	if !strings.Contains(msg, "claude-code") {
		t.Errorf("error must name the supporting host (so user sees WHERE it works); got %q", msg)
	}
	if !strings.Contains(msg, "--allow-lossy") {
		t.Errorf("error must suggest --allow-lossy; got %q", msg)
	}
}

func TestInstall_SkillWithAllowedTools_OnGeminiCLI_AllowLossyDropsField(t *testing.T) {
	// Counterpart to the refusal test: with --allow-lossy, install
	// succeeds AND the emitted SKILL.md does NOT carry `allowed-tools`
	// (it's stripped per schema.HostKeepsExtension returning false on gemini-cli). The lossy gate
	// is honest about what dropped — not just a "proceed anyway" flag.
	geminiHome, _ := setupGeminiEnv(t)

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "claudish-skill-2")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(skillDir, "SKILL.md")
	body := []byte(`---
name: claudish-skill-2
description: short description text
allowed-tools: Read, Write, Edit
---
body content
`)
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--allow-lossy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install with --allow-lossy: %v", err)
	}

	target := filepath.Join(geminiHome, "skills", "claudish-skill-2", "SKILL.md")
	emitted, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	// Assert the `allowed-tools:` KEY is absent — not just the substring,
	// which would false-positive against any description text containing
	// the phrase. The emit must drop the key entirely on gemini-cli.
	if bytes.Contains(emitted, []byte("\nallowed-tools:")) ||
		bytes.HasPrefix(bytes.TrimPrefix(emitted, []byte("---\n")), []byte("allowed-tools:")) {
		t.Errorf("emitted SKILL.md must NOT carry allowed-tools key (dropped on gemini-cli); got:\n%s",
			string(emitted))
	}
	if !bytes.Contains(emitted, []byte("name: claudish-skill-2")) {
		t.Errorf("emitted SKILL.md must preserve universal core; got:\n%s", string(emitted))
	}
}

func TestInstall_AgentWithTemperature_OnGeminiCLI_NotLossy(t *testing.T) {
	// DISCRIMINATING TEST 2 (per advisor): positive control. Agent
	// carrying `temperature: 0.5` (gemini_agent_runtime_overrides,
	// supported on gemini-cli) → install onto gemini-cli succeeds with
	// NO --allow-lossy. Emitted file preserves temperature. The inverse
	// of TestSchemaLossy_AgentTemperatureLossyOnClaudeCode (which was
	// the only side of the §8 firing the codebase tested before #7).
	geminiHome, _ := setupGeminiEnv(t)

	tmp := t.TempDir()
	src := filepath.Join(tmp, "gemini-agent.md")
	body := []byte(`---
name: gemini-agent
description: agent with gemini-native temperature
temperature: 0.5
---
agent body
`)
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--kind", "agent"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install (positive control, should NOT need --allow-lossy): %v", err)
	}

	target := filepath.Join(geminiHome, "agents", "gemini-agent.md")
	emitted, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	if !bytes.Contains(emitted, []byte("temperature")) {
		t.Errorf("emitted agent must preserve temperature (gemini-native); got:\n%s", string(emitted))
	}
}

func TestList_MixedHost_ShowsBoth(t *testing.T) {
	// Mixed-host smoke: install skill `foo` on both claude-code and
	// gemini-cli. Both records appear in list output with distinct IDs.
	// Tests the ID-uniqueness story across hosts.
	claudeHome := t.TempDir()
	geminiHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	for _, args := range [][]string{
		{"install", src, "--agent", "claude-code"},
		{"install", src, "--agent", "gemini-cli"},
	} {
		cmd := NewRootCmd()
		cmd.SetOut(io_DiscardWriter())
		cmd.SetErr(io_DiscardWriter())
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("setup install %v: %v", args, err)
		}
	}

	var out bytes.Buffer
	list := NewRootCmd()
	list.SetOut(&out)
	list.SetErr(&out)
	list.SetArgs([]string{"list"})
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "claude-code:skill:dotpack-tracer-bullet") {
		t.Errorf("list missing claude-code row; got %q", got)
	}
	if !strings.Contains(got, "gemini-cli:skill:dotpack-tracer-bullet") {
		t.Errorf("list missing gemini-cli row; got %q", got)
	}
}

func TestUninstall_OnGeminiCLIOnly_DefaultAgentClaudeCode_HintsActualID(t *testing.T) {
	// DISCRIMINATING TEST 4 (per advisor): install a skill ONLY on
	// gemini-cli, then `dotpack uninstall <short-name>` (defaults --agent
	// claude-code, --kind skill) → composes claude-code:skill:<name>,
	// misses, and the error hints `gemini-cli:skill:<name>`. The hint
	// logic compares short-names regardless of host, so this should
	// "just work" — pin it so a future change to the hint logic doesn't
	// silently regress cross-host disambiguation.
	claudeHome := t.TempDir()
	geminiHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "gemini-cli"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install on gemini-cli: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	// Default --agent claude-code, --kind skill → composes
	// "claude-code:skill:dotpack-tracer-bullet" → manifest miss.
	uninstall.SetArgs([]string{"uninstall", "dotpack-tracer-bullet"})
	err := uninstall.Execute()
	if err == nil {
		t.Fatal("expected uninstall to fail (gemini-cli only, default --agent claude-code), got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean") {
		t.Errorf("error must hint at the actual ID; got %q", msg)
	}
	if !strings.Contains(msg, "gemini-cli:skill:dotpack-tracer-bullet") {
		t.Errorf("hint must surface the cross-host ID; got %q", msg)
	}
}

func TestUninstall_AgentOnGeminiCLIOnly_DefaultsMisroute_HintsCrossHostAndCrossKind(t *testing.T) {
	// Companion to TestUninstall_OnGeminiCLIOnly_DefaultAgentClaudeCode_HintsActualID:
	// install an AGENT only on gemini-cli, then `dotpack uninstall foo`
	// (defaults --agent claude-code --kind skill) → composes
	// "claude-code:skill:foo" → misses → hint must surface
	// "gemini-cli:agent:foo". Covers the cross-(host, kind) cell of the
	// hint-disambiguation matrix that the skill-on-gemini test left open
	// — pins that the hint logic is both host-agnostic AND kind-agnostic.
	geminiHome, _ := setupGeminiEnv(t)

	tmp := t.TempDir()
	src := filepath.Join(tmp, "lonely-agent.md")
	body := []byte(`---
name: lonely-agent
description: agent installed only on gemini-cli
---
agent body
`)
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--kind", "agent"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install on gemini-cli: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	// Defaults to --agent claude-code --kind skill → both axes wrong.
	uninstall.SetArgs([]string{"uninstall", "lonely-agent"})
	err := uninstall.Execute()
	if err == nil {
		t.Fatal("expected uninstall to fail (cross-host + cross-kind misroute), got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean") {
		t.Errorf("error must hint at the actual ID; got %q", msg)
	}
	if !strings.Contains(msg, "gemini-cli:agent:lonely-agent") {
		t.Errorf("hint must surface the cross-host AND cross-kind ID; got %q", msg)
	}

	// Sanity: the agent file is still on disk (failed uninstall did
	// not partially execute against the wrong target).
	if _, err := os.Stat(filepath.Join(geminiHome, "agents", "lonely-agent.md")); err != nil {
		t.Errorf("agent file must survive a misrouted uninstall; got %v", err)
	}
}

func TestUninstall_OnGeminiCLI_ByFullID_RemovesFile(t *testing.T) {
	// Round out the mixed-host coverage: a full-ID uninstall on
	// gemini-cli works, regardless of default --agent. The handle
	// containing ":" bypasses --agent resolution per resolveUninstallID.
	geminiHome, _ := setupGeminiEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "gemini-cli"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "gemini-cli:skill:dotpack-tracer-bullet"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall by full ID: %v", err)
	}

	target := filepath.Join(geminiHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone after uninstall; stat err: %v", err)
	}
}
