package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupCodexEnv installs DOTPACK_* env vars pointing at fresh tempdirs
// for each major root, with AgentsHome wired (codex's native skill
// root). ProjectHome is set too so project-scope branches have a valid
// target if exercised.
func setupCodexEnv(t *testing.T) (agentsHome, dotpackHome string) {
	t.Helper()
	agentsHome = t.TempDir()
	dotpackHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", agentsHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	return agentsHome, dotpackHome
}

func TestInstall_Skill_OnCodex_EndToEnd(t *testing.T) {
	// Smoke test: the tracer-bullet skill (universal-core only) installs
	// onto codex without --allow-lossy. Confirms --agent codex is wired
	// and writes to AgentsHome/skills/<name>/SKILL.md (codex's only
	// documented native path per developers.openai.com/codex/skills).
	agentsHome, _ := setupCodexEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--scope", "user"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "Installed codex:skill:dotpack-tracer-bullet") {
		t.Errorf("expected success message; got %q", got)
	}
	target := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected SKILL.md at %s: %v", target, err)
	}
}

func TestInstall_SkillWithAllowedTools_OnCodex_LossyRefused(t *testing.T) {
	// Third real-host §8 firing (after claudecode + gemini-cli). A skill
	// carrying `allowed-tools` (bound to claude_skill_runtime_overrides,
	// supported only on claude-code) → install --agent codex with no
	// --allow-lossy must refuse with LossyError. Same schema-driven
	// algorithm, third host, no per-host branching needed.
	setupCodexEnv(t)

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "claudish-codex-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(skillDir, "SKILL.md")
	body := []byte(`---
name: claudish-codex-skill
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
	cmd.SetArgs([]string{"install", src, "--agent", "codex"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected LossyError for allowed-tools on codex, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "codex") {
		t.Errorf("error must name the lossy host; got %q", msg)
	}
	if !strings.Contains(msg, "allowed-tools") {
		t.Errorf("error must name the lossy field; got %q", msg)
	}
	if !strings.Contains(msg, "claude_skill_runtime_overrides") {
		t.Errorf("error must name the canonical concept; got %q", msg)
	}
	if !strings.Contains(msg, "claude-code") {
		t.Errorf("error must name the supporting host; got %q", msg)
	}
	if !strings.Contains(msg, "--allow-lossy") {
		t.Errorf("error must suggest --allow-lossy; got %q", msg)
	}
}

func TestInstall_SkillWithAllowedTools_OnCodex_AllowLossyDropsField(t *testing.T) {
	// Counterpart to the refusal test: with --allow-lossy, install
	// succeeds AND the emitted SKILL.md has `allowed-tools` stripped
	// per codexKeeps. Assert key-form, not substring (description in
	// fixture is deliberately benign — but the key-form pattern guards
	// against future fixture drift containing the phrase in prose).
	agentsHome, _ := setupCodexEnv(t)

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "claudish-codex-skill-2")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(skillDir, "SKILL.md")
	body := []byte(`---
name: claudish-codex-skill-2
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
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--allow-lossy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install with --allow-lossy: %v", err)
	}

	target := filepath.Join(agentsHome, "skills", "claudish-codex-skill-2", "SKILL.md")
	emitted, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	if bytes.Contains(emitted, []byte("\nallowed-tools:")) ||
		bytes.HasPrefix(bytes.TrimPrefix(emitted, []byte("---\n")), []byte("allowed-tools:")) {
		t.Errorf("emitted SKILL.md must NOT carry allowed-tools key (dropped on codex); got:\n%s",
			string(emitted))
	}
	if !bytes.Contains(emitted, []byte("name: claudish-codex-skill-2")) {
		t.Errorf("emitted SKILL.md must preserve universal core; got:\n%s", string(emitted))
	}
}

func TestInstall_AgentOnCodex_ReturnsUnsupportedError(t *testing.T) {
	// Codex declares Capabilities[KindAgent] absent (no native codex
	// agent loading path per OpenAI docs); Plan returns "codex: kind
	// agent not yet supported". CLI must surface that as a normal error
	// (not panic, not silent success), so the user sees an actionable
	// message instead of fishing in the manifest for a write that never
	// happened. Pins the contract: CLI does NOT pre-filter on
	// Capabilities — the adapter's Plan is the enforcement point.
	setupCodexEnv(t)

	tmp := t.TempDir()
	src := filepath.Join(tmp, "would-be-codex-agent.md")
	body := []byte(`---
name: would-be-codex-agent
description: an agent that codex cannot host
---
agent body
`)
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "agent"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for agent kind on codex (KindAgent unsupported), got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "codex") {
		t.Errorf("error must name the host; got %q", msg)
	}
	if !strings.Contains(msg, "agent") {
		t.Errorf("error must name the unsupported kind; got %q", msg)
	}
}

func TestList_AllThreeHosts_ShowsAll(t *testing.T) {
	// Three-host smoke: install skill `foo` on claude-code, gemini-cli,
	// AND codex; all three records appear in `list` output with distinct
	// IDs. Tests the ID-uniqueness story across the now-full host set.
	claudeHome := t.TempDir()
	geminiHome := t.TempDir()
	agentsHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_AGENTS_HOME", agentsHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	for _, args := range [][]string{
		{"install", src, "--agent", "claude-code"},
		{"install", src, "--agent", "gemini-cli"},
		{"install", src, "--agent", "codex"},
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
	for _, want := range []string{
		"claude-code:skill:dotpack-tracer-bullet",
		"gemini-cli:skill:dotpack-tracer-bullet",
		"codex:skill:dotpack-tracer-bullet",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("list missing row %q; got %q", want, got)
		}
	}
}

func TestUninstall_OnCodexOnly_DefaultAgentClaudeCode_HintsActualID(t *testing.T) {
	// Mirror of TestUninstall_OnGeminiCLIOnly_DefaultAgentClaudeCode_HintsActualID
	// for the third host: install a skill ONLY on codex, then
	// `dotpack uninstall <short-name>` defaults to --agent claude-code,
	// composes claude-code:skill:<name>, misses, and the error must hint
	// `codex:skill:<name>`. Pins that the cross-host disambiguation
	// from slice 3 task #6 is now-host-count-agnostic (works for all
	// three hosts without code changes).
	claudeHome := t.TempDir()
	geminiHome := t.TempDir()
	agentsHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_AGENTS_HOME", agentsHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "codex"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install on codex: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "dotpack-tracer-bullet"})
	err := uninstall.Execute()
	if err == nil {
		t.Fatal("expected uninstall to fail (codex only, default --agent claude-code), got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean") {
		t.Errorf("error must hint at the actual ID; got %q", msg)
	}
	if !strings.Contains(msg, "codex:skill:dotpack-tracer-bullet") {
		t.Errorf("hint must surface the codex ID; got %q", msg)
	}
}

func TestUninstall_OnCodex_ByFullID_RemovesFile(t *testing.T) {
	// Full-ID uninstall on codex works and reclaims the per-name dir.
	// Mirror of TestUninstall_OnGeminiCLI_ByFullID_RemovesFile.
	agentsHome, _ := setupCodexEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "codex"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "codex:skill:dotpack-tracer-bullet"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall by full ID: %v", err)
	}

	target := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone after uninstall; stat err: %v", err)
	}
	// Per-name subdir reclaimed (TargetDir contract).
	dir := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("per-name dir should be reclaimed after uninstall; stat err: %v", err)
	}
}

func TestInstall_SkillOnCodexThenGeminiCLI_NoCollisionAtConvergencePath(t *testing.T) {
	// Codex writes to AgentsHome/skills/<name>/; gemini-cli writes to
	// GeminiHome/skills/<name>/. Different paths → no collision today,
	// even though gemini-cli ALSO reads from AgentsHome/skills/ as a
	// convergence path. Pins the codex package docstring claim: the
	// gemini-cli adapter deliberately defers AgentsHome to keep this
	// invariant. If a future change makes gemini-cli write to AgentsHome
	// too, the second install would CollisionError against the
	// manifest-tracked codex write — Fatalf on install error makes that
	// fail loudly.
	//
	// Roots wired explicitly rather than via setupCodexEnv + os.Getenv
	// roundtrip — re-reading an env var the helper set is brittle
	// plumbing (hostile-review #3).
	claudeHome := t.TempDir()
	geminiHome := t.TempDir()
	agentsHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_AGENTS_HOME", agentsHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	for _, args := range [][]string{
		{"install", src, "--agent", "codex"},
		{"install", src, "--agent", "gemini-cli"},
	} {
		cmd := NewRootCmd()
		cmd.SetOut(io_DiscardWriter())
		cmd.SetErr(io_DiscardWriter())
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("install %v: %v", args, err)
		}
	}

	// Both files exist at their host-native paths.
	codexPath := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(codexPath); err != nil {
		t.Errorf("codex install missing at %s: %v", codexPath, err)
	}
	geminiPath := filepath.Join(geminiHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(geminiPath); err != nil {
		t.Errorf("gemini-cli install missing at %s: %v", geminiPath, err)
	}
}
