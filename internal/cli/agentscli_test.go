package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// setupAgentsCliEnv mirrors setupCodexEnv but is named for the umbrella
// it exercises. AgentsHome is the convergence path both gemini-cli and
// codex read; it's where --agent agents-cli writes once for the skill
// kind per ADR-0012 §1. ClaudeHome and GeminiHome are still set so the
// sub-adapters that the umbrella constructs (codex + gemini-cli) can
// validate their UserRoot accessors without erroring during the lossy
// pre-flight — both sub-adapters' Plan() is potentially called when
// lossy aggregation happens, even though only one Plan() supplies the
// actual file write.
func setupAgentsCliEnv(t *testing.T) (agentsHome, dotpackHome string) {
	t.Helper()
	agentsHome = t.TempDir()
	dotpackHome = t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_GEMINI_HOME", t.TempDir())
	t.Setenv("DOTPACK_ANTIGRAVITY_HOME", t.TempDir())
	t.Setenv("DOTPACK_AGENTS_HOME", agentsHome)
	t.Setenv("DOTPACK_CODEX_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	return agentsHome, dotpackHome
}

// TestInstall_Skill_OnAgentsCli_EndToEnd is the primary tracer-bullet
// vertical slice for the agents-cli umbrella per ADR-0012 §1. It pins:
//
//   - --agent agents-cli is a recognised, buildable flag value (no longer
//     the "not yet implemented" sentinel from architecture-review Card #4).
//   - For skill kind, write-once convergence to AgentsHome/skills/<name>/
//     SKILL.md (codex's documented native path per developers.openai.com/
//     codex/skills; gemini-cli ALSO reads ~/.agents/skills/ per schema/
//     skill.yaml ecosystem_notes, so the single write is consumed by both
//     runtimes).
//   - Manifest record carries Agent="agents-cli" (NOT a sub-adapter HostID)
//     and ID=agents-cli:skill:<name>. The umbrella IS the user-visible
//     identity; the sub-adapter set lives in the orchestrator's
//     CLI-flag-to-adapter-set map (ADR-0012 §10), not on the record.
//   - Exactly one file is written. The umbrella does not invoke both
//     sub-adapters with redundant writes (would CollisionError on the
//     second one and break the umbrella's "install once, all CLIs see it"
//     contract).
func TestInstall_Skill_OnAgentsCli_EndToEnd(t *testing.T) {
	agentsHome, dotpackHome := setupAgentsCliEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--scope", "user"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install --agent agents-cli: %v\n%s", err, stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "Installed agents-cli:skill:dotpack-tracer-bullet") {
		t.Errorf("expected umbrella-prefixed success message; got %q", got)
	}

	target := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected SKILL.md at convergence path %s: %v", target, err)
	}

	// The umbrella's write-once contract: no per-sub-adapter mirror at
	// GeminiHome/skills/. If a future change accidentally fans out the
	// file-drop kind, this pin catches it.
	geminiPath := filepath.Join(os.Getenv("DOTPACK_GEMINI_HOME"), "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(geminiPath); err == nil {
		t.Errorf("umbrella must NOT write a redundant copy at gemini-cli's host path; found %s", geminiPath)
	}

	// Manifest record assertions: Agent and ID must carry the umbrella
	// identity, not a sub-adapter's HostID. The "user typed --agent
	// agents-cli, list shows agents-cli" contract.
	manifestPath := filepath.Join(dotpackHome, "installs.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Installs []struct {
			ID    string `yaml:"id"`
			Agent string `yaml:"agent"`
			Kind  string `yaml:"kind"`
			Files []string
		} `yaml:"installs"`
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, string(raw))
	}
	if len(m.Installs) != 1 {
		t.Fatalf("expected exactly 1 manifest record; got %d:\n%s", len(m.Installs), string(raw))
	}
	rec := m.Installs[0]
	if rec.ID != "agents-cli:skill:dotpack-tracer-bullet" {
		t.Errorf("record ID must be umbrella-prefixed; got %q", rec.ID)
	}
	if rec.Agent != "agents-cli" {
		t.Errorf("record Agent must be the umbrella label; got %q", rec.Agent)
	}
	if rec.Kind != "skill" {
		t.Errorf("record Kind must be skill; got %q", rec.Kind)
	}
	if len(rec.Files) != 1 || rec.Files[0] != target {
		t.Errorf("record Files must be exactly [%s]; got %v", target, rec.Files)
	}
}

// TestInstall_SkillWithAllowedTools_OnAgentsCli_LossyRefused pins the
// schema-driven lossy check under the umbrella per ADR-0012 §8 literal
// aggregation: a field whose canonical_concept is supported on NEITHER
// sub-adapter (gemini-cli, codex) — here claude_skill_runtime_overrides
// is claude-code-only — must refuse install without --allow-lossy. The
// error names the umbrella as the lossy host, not a sub-adapter,
// because the user typed --agent agents-cli and that's the identity the
// failure should reference.
func TestInstall_SkillWithAllowedTools_OnAgentsCli_LossyRefused(t *testing.T) {
	setupAgentsCliEnv(t)

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "claudish-umbrella-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(skillDir, "SKILL.md")
	body := []byte(`---
name: claudish-umbrella-skill
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
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected LossyError for allowed-tools on agents-cli, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "agents-cli") {
		t.Errorf("error must name the umbrella as the lossy host; got %q", msg)
	}
	if !strings.Contains(msg, "allowed-tools") {
		t.Errorf("error must name the lossy field; got %q", msg)
	}
	if !strings.Contains(msg, "claude_skill_runtime_overrides") {
		t.Errorf("error must name the canonical concept; got %q", msg)
	}
	if !strings.Contains(msg, "--allow-lossy") {
		t.Errorf("error must suggest --allow-lossy; got %q", msg)
	}
}

// TestInstall_SkillWithAllowedTools_OnAgentsCli_AllowLossyPreservesSource is
// the counterpart to the refusal test: with --allow-lossy, install succeeds
// while ADR-0004 source identity remains intact. Neither Gemini nor Codex
// honours allowed-tools, so the install remains semantically lossy even though
// dotpack preserves the authorial bytes.
func TestInstall_SkillWithAllowedTools_OnAgentsCli_AllowLossyPreservesSource(t *testing.T) {
	agentsHome, _ := setupAgentsCliEnv(t)

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "claudish-umbrella-skill-2")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := filepath.Join(skillDir, "SKILL.md")
	body := []byte(`---
name: claudish-umbrella-skill-2
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
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--allow-lossy"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install with --allow-lossy: %v", err)
	}

	target := filepath.Join(agentsHome, "skills", "claudish-umbrella-skill-2", "SKILL.md")
	emitted, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read emitted: %v", err)
	}
	if !bytes.Equal(emitted, body) {
		t.Errorf("emitted SKILL.md must preserve source bytes under --allow-lossy; got:\n%s",
			string(emitted))
	}
}

func TestInstall_SealedSkill_OnAgentsCli_AllowLossyPreservesSeal(t *testing.T) {
	agentsHome, _ := setupAgentsCliEnv(t)

	skillDir := filepath.Join(t.TempDir(), "sealed-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := []byte(`---
name: sealed-skill
description: sealed skill with host-specific authorial intent
compatibility: Requires git
disable-model-invocation: true
---
sealed body
`)
	sourceDigest := sha256.Sum256(source)
	provenance := []byte(fmt.Sprintf("SKILL.md sha256:%x\n", sourceDigest))
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), source, 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "PROVENANCE.md"), provenance, 0o644); err != nil {
		t.Fatalf("write PROVENANCE.md: %v", err)
	}

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install",
		filepath.Join(skillDir, "SKILL.md"),
		"--agent", "agents-cli",
		"--allow-lossy",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install sealed skill with --allow-lossy: %v", err)
	}

	targetDir := filepath.Join(agentsHome, "skills", "sealed-skill")
	installedSkill, err := os.ReadFile(filepath.Join(targetDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed SKILL.md: %v", err)
	}
	installedProvenance, err := os.ReadFile(filepath.Join(targetDir, "PROVENANCE.md"))
	if err != nil {
		t.Fatalf("read installed PROVENANCE.md: %v", err)
	}
	installedDigest := sha256.Sum256(installedSkill)
	wantProvenance := []byte(fmt.Sprintf("SKILL.md sha256:%x\n", installedDigest))
	if !bytes.Equal(installedProvenance, wantProvenance) {
		t.Fatalf("installed package invalidated its own seal:\nrecorded: %sactual:   %s",
			string(installedProvenance), string(wantProvenance))
	}
}

// TestUninstall_AgentsCli_RoundTrips pins symmetric install/uninstall
// using the umbrella ID. Uninstall doesn't traverse an adapter today
// (orchestrator.Reader works from manifest absolute paths per Card #3's
// split), so the umbrella ID round-trips through Reader without
// umbrella-specific code on the uninstall path. This is the no-code
// payoff of recording Agent="agents-cli" on install: the ID alone
// addresses the install.
func TestUninstall_AgentsCli_RoundTrips(t *testing.T) {
	agentsHome, _ := setupAgentsCliEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "agents-cli"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "agents-cli:skill:dotpack-tracer-bullet"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall by umbrella ID: %v", err)
	}

	target := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone after umbrella uninstall; stat err: %v", err)
	}
	dir := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("per-name dir should be reclaimed; stat err: %v", err)
	}
}

// TestInstall_SkillOnCodexThenAgentsCli_CollidesAtConvergencePath pins
// the cross-flag identity contract per Option A (this session's design
// decision): --agent codex and --agent agents-cli produce DIFFERENT
// manifest IDs (codex:skill:foo vs agents-cli:skill:foo) even though
// they write to the same path. The second install must surface as a
// collision — both records cannot silently coexist on disk. The user
// must explicitly uninstall the first or pass --force.
//
// The alternative (option C: share codex's ID) would silently
// in-place-overwrite. The user-facing identity is the umbrella the user
// typed; option A keeps that visible in the manifest, with the cost that
// the manifest never tracks two installs at the same path.
func TestInstall_SkillOnCodexThenAgentsCli_CollidesAtConvergencePath(t *testing.T) {
	setupAgentsCliEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	first := NewRootCmd()
	first.SetOut(io_DiscardWriter())
	first.SetErr(io_DiscardWriter())
	first.SetArgs([]string{"install", src, "--agent", "codex"})
	if err := first.Execute(); err != nil {
		t.Fatalf("first install (codex): %v", err)
	}

	second := NewRootCmd()
	second.SetOut(io_DiscardWriter())
	second.SetErr(io_DiscardWriter())
	second.SetArgs([]string{"install", src, "--agent", "agents-cli"})
	err := second.Execute()
	if err == nil {
		t.Fatal("expected CollisionError on second install via different --agent flag, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "collide") {
		t.Errorf("error must surface as a collision; got %q", msg)
	}
	if !strings.Contains(msg, "--force") {
		t.Errorf("error must suggest --force; got %q", msg)
	}
}

// TestInstall_Skill_OnAgentsCli_ProjectScope pins the project-scope
// path through the umbrella. The canonical writer (codex) routes
// project scope to <ProjectHome>/.agents/skills/<name>/SKILL.md per
// its filedrop layout (ProjectSubdir=".agents"). Without this test
// the project-scope path through runUmbrellaInstall is unverified —
// it goes through a distinct adapter.ScopeProject branch in
// filedrop.targetPath and would silently regress if a future change
// to UmbrellaInstaller.Install hardcoded user scope.
func TestInstall_Skill_OnAgentsCli_ProjectScope(t *testing.T) {
	setupAgentsCliEnv(t)
	projectHome := os.Getenv("DOTPACK_PROJECT_HOME")
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--scope", "project"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install --scope project: %v", err)
	}
	target := filepath.Join(projectHome, ".agents", "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected SKILL.md at project-scope path %s: %v", target, err)
	}
	// User-scope path must NOT be written.
	userPath := filepath.Join(os.Getenv("DOTPACK_AGENTS_HOME"), "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(userPath); err == nil {
		t.Errorf("project-scope install must NOT write user-scope path; found %s", userPath)
	}
}

func TestInstall_SkillOnAgentsCli_ReinstallRemovesFilesAbsentFromNewSource(t *testing.T) {
	setupAgentsCliEnv(t)
	projectHome := os.Getenv("DOTPACK_PROJECT_HOME")

	writeSource := func(root string, includeExtra bool) string {
		t.Helper()
		skillDir := filepath.Join(root, "demo")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("mkdir source skill: %v", err)
		}
		skillPath := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: d\n---\n# demo\n"), 0o644); err != nil {
			t.Fatalf("write source SKILL.md: %v", err)
		}
		if includeExtra {
			if err := os.WriteFile(filepath.Join(skillDir, "extra.py"), []byte("print(1)\n"), 0o644); err != nil {
				t.Fatalf("write source support file: %v", err)
			}
		}
		return skillPath
	}

	sourceA := writeSource(t.TempDir(), true)
	sourceB := writeSource(t.TempDir(), false)
	for _, source := range []string{sourceA, sourceB} {
		cmd := NewRootCmd()
		cmd.SetOut(io_DiscardWriter())
		cmd.SetErr(io_DiscardWriter())
		cmd.SetArgs([]string{"install", source, "--agent", "agents-cli", "--scope", "project"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("install %s: %v", source, err)
		}
	}

	extra := filepath.Join(projectHome, ".agents", "skills", "demo", "extra.py")
	if _, err := os.Stat(extra); !os.IsNotExist(err) {
		t.Fatalf("reinstall must remove prior-owned file absent from new source; stat %s: %v", extra, err)
	}
}

// TestInstall_SkillOnCodexThenAgentsCli_ForceProducesDualRecords pins
// the recovery contract for the cross-flag forced-overwrite case.
// Sequence: `--agent codex foo` writes codex:skill:foo → file at
// AgentsHome/skills/foo/SKILL.md. Then `--agent agents-cli foo --force`
// overwrites the same file under a DIFFERENT manifest ID
// (agents-cli:skill:foo). Now TWO manifest records point at one file.
//
// This is messy but recoverable: uninstall agents-cli:skill:foo →
// file vanishes → codex:skill:foo's manifest is orphan-ish.
// Subsequent uninstall codex:skill:foo finds the file already gone,
// reports "skipped (already gone)", and removes the codex record.
// Reader.Uninstall's tolerance of missing files (orchestrator.go:316
// case os.IsNotExist) is what makes this work — a future change that
// errors on missing files would break this recovery path.
//
// The test exists not to bless the dual-record state as a desirable
// outcome, but to PIN that recovery is possible. The collision-refuse
// path (no --force) is the recommended UX (pinned by
// TestInstall_SkillOnCodexThenAgentsCli_CollidesAtConvergencePath);
// this test covers what happens when the user knowingly overrides.
func TestInstall_SkillOnCodexThenAgentsCli_ForceProducesDualRecords(t *testing.T) {
	agentsHome, dotpackHome := setupAgentsCliEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	first := NewRootCmd()
	first.SetOut(io_DiscardWriter())
	first.SetErr(io_DiscardWriter())
	first.SetArgs([]string{"install", src, "--agent", "codex"})
	if err := first.Execute(); err != nil {
		t.Fatalf("first install (codex): %v", err)
	}

	second := NewRootCmd()
	second.SetOut(io_DiscardWriter())
	second.SetErr(io_DiscardWriter())
	second.SetArgs([]string{"install", src, "--agent", "agents-cli", "--force"})
	if err := second.Execute(); err != nil {
		t.Fatalf("second install (agents-cli --force): %v", err)
	}

	// Manifest now has BOTH records.
	manifestPath := filepath.Join(dotpackHome, "installs.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "id: codex:skill:dotpack-tracer-bullet") {
		t.Errorf("manifest missing codex record after --force; got:\n%s", body)
	}
	if !strings.Contains(body, "id: agents-cli:skill:dotpack-tracer-bullet") {
		t.Errorf("manifest missing agents-cli record after --force; got:\n%s", body)
	}

	target := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("convergence file must exist after --force: %v", err)
	}

	// Recovery step 1: uninstall agents-cli record. File vanishes;
	// codex record now orphan.
	un1 := NewRootCmd()
	un1.SetOut(io_DiscardWriter())
	un1.SetErr(io_DiscardWriter())
	un1.SetArgs([]string{"uninstall", "agents-cli:skill:dotpack-tracer-bullet"})
	if err := un1.Execute(); err != nil {
		t.Fatalf("uninstall agents-cli record: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone after first uninstall; stat err: %v", err)
	}

	// Recovery step 2: uninstall codex record. File already gone →
	// MissingPaths populated, no error, record removed.
	var un2out bytes.Buffer
	un2 := NewRootCmd()
	un2.SetOut(&un2out)
	un2.SetErr(&un2out)
	un2.SetArgs([]string{"uninstall", "codex:skill:dotpack-tracer-bullet"})
	if err := un2.Execute(); err != nil {
		t.Fatalf("recovery uninstall of orphan codex record: %v", err)
	}
	if !strings.Contains(un2out.String(), "skipped") {
		t.Errorf("orphan uninstall should surface 'skipped (already gone)'; got %q", un2out.String())
	}

	// Manifest is now empty.
	raw, err = os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("re-read manifest: %v", err)
	}
	if strings.Contains(string(raw), "dotpack-tracer-bullet") {
		t.Errorf("manifest must be empty after both uninstalls; got:\n%s", string(raw))
	}
}

func TestInstall_AgentKindOnAgentsCli_Supported(t *testing.T) {
	setupAgentsCliEnv(t)
	src := filepath.Join("..", "resource", "testdata", "agents", "dotpack-tracer-agent.md")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "agents-cli", "--kind", "agent"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("expected install to succeed for agent kind on agents-cli, got %v", err)
	}

	geminiPath := filepath.Join(os.Getenv("DOTPACK_GEMINI_HOME"), "agents", "dotpack-tracer-agent.md")
	if _, err := os.Stat(geminiPath); err != nil {
		t.Errorf("expected agent file at gemini path %s: %v", geminiPath, err)
	}

	codexPath := filepath.Join(os.Getenv("DOTPACK_CODEX_HOME"), "agents", "dotpack-tracer-agent.toml")
	if _, err := os.Stat(codexPath); err != nil {
		t.Errorf("expected TOML agent file at codex path %s: %v", codexPath, err)
	}
}

// TestInstall_CommandAndMemory_OnAgentsCli_FanOut pins the ADR-0014
// extension: command and memory — previously unsupported under the
// umbrella — now fan out to each sub-adapter's own distinct file, and
// uninstall removes all of them. This is the regression guard for the
// previously-closed agents-cli matrix being opened.
func TestInstall_CommandAndMemory_OnAgentsCli_FanOut(t *testing.T) {
	setupAgentsCliEnv(t)
	geminiHome := os.Getenv("DOTPACK_GEMINI_HOME")
	antigravityHome := os.Getenv("DOTPACK_ANTIGRAVITY_HOME")
	codexHome := os.Getenv("DOTPACK_CODEX_HOME")
	tmp := t.TempDir()

	cmdSrc := filepath.Join(tmp, ".agents", "commands", "deploy.md")
	if err := os.MkdirAll(filepath.Dir(cmdSrc), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cmdSrc, []byte("---\ndescription: d\n---\nrun the deploy\n"), 0o644); err != nil {
		t.Fatalf("write command: %v", err)
	}
	runDotpack(t, "install", cmdSrc, "--agent", "agents-cli", "--kind", "command", "--scope", "user")
	commandTargets := []string{
		filepath.Join(geminiHome, "commands", "deploy.toml"),
		filepath.Join(antigravityHome, "commands", "deploy.md"),
		filepath.Join(codexHome, "commands", "deploy.md"),
	}
	for _, p := range commandTargets {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected command fan-out file %s: %v", p, err)
		}
	}

	memSrc := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(memSrc, []byte("remember this\n"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	runDotpack(t, "install", memSrc, "--agent", "agents-cli", "--kind", "memory", "--scope", "user")
	memoryTargets := []string{
		filepath.Join(geminiHome, "GEMINI.md"),
		filepath.Join(antigravityHome, "ANTIGRAVITY.md"),
		filepath.Join(codexHome, "AGENTS.md"),
	}
	for _, p := range memoryTargets {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected memory fan-out file %s: %v", p, err)
		}
	}

	runDotpack(t, "uninstall", "agents-cli:command:deploy")
	for _, p := range commandTargets {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("command fan-out file survived uninstall: %s", p)
		}
	}
	runDotpack(t, "uninstall", "agents-cli:memory:AGENTS.md")
	for _, p := range memoryTargets {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("memory fan-out file survived uninstall: %s", p)
		}
	}
}

// runDotpack executes one dotpack command through the cobra root, failing
// the test on error.
func runDotpack(t *testing.T, args ...string) {
	t.Helper()
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack %v: %v\n%s", args, err, out.String())
	}
}
