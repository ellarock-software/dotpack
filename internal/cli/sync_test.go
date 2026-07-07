package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInstall_ProjectScopeManifestRecordsTargetAndFileClaim(t *testing.T) {
	configRoot, agentsRoot := writeCanonicalSkill(t, "daily-skill", "original\n")
	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join(agentsRoot, "skills", "daily-skill", "SKILL.md")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Installs []struct {
			ID            string `yaml:"id"`
			CanonicalRoot string `yaml:"canonical_root"`
			TargetRoot    string `yaml:"target_root"`
			SourceRelPath string `yaml:"source_rel_path"`
			FileClaims    []struct {
				Path   string `yaml:"path"`
				SHA256 string `yaml:"sha256"`
			} `yaml:"file_claims"`
		} `yaml:"installs"`
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, raw)
	}
	if len(m.Installs) != 1 {
		t.Fatalf("manifest installs: got %d, want 1", len(m.Installs))
	}
	rec := m.Installs[0]
	if rec.CanonicalRoot != agentsRoot {
		t.Errorf("canonical_root = %q, want %q (config root %s)", rec.CanonicalRoot, agentsRoot, configRoot)
	}
	if rec.TargetRoot != targetRoot {
		t.Errorf("target_root = %q, want %q", rec.TargetRoot, targetRoot)
	}
	if rec.SourceRelPath != "skills/daily-skill/SKILL.md" {
		t.Errorf("source_rel_path = %q", rec.SourceRelPath)
	}
	if len(rec.FileClaims) != 1 || rec.FileClaims[0].SHA256 == "" {
		t.Fatalf("file_claims missing hash: %+v", rec.FileClaims)
	}
	installedRaw, err := os.ReadFile(rec.FileClaims[0].Path)
	if err != nil {
		t.Fatalf("read installed file: %v", err)
	}
	if rec.FileClaims[0].SHA256 != sha256ForTest(installedRaw) {
		t.Errorf("file claim hash does not match installed bytes")
	}
}

func TestInstall_SkillCopiesSupportingFilesAndRecordsClaims(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "daily-skill", "See references/guide.md and scripts/run.sh.\n")
	skillDir := filepath.Join(agentsRoot, "skills", "daily-skill")
	mustWriteTestFile(t, filepath.Join(skillDir, "references", "guide.md"), "# Guide\n\nSupport content.\n")
	scriptPath := filepath.Join(skillDir, "scripts", "run.sh")
	mustWriteTestFile(t, scriptPath, "#!/bin/sh\nprintf support\\n\n")
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatalf("chmod support script: %v", err)
	}

	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join(skillDir, "SKILL.md")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	wantFiles := []string{
		filepath.Join(targetRoot, ".claude", "skills", "daily-skill", "SKILL.md"),
		filepath.Join(targetRoot, ".claude", "skills", "daily-skill", "references", "guide.md"),
		filepath.Join(targetRoot, ".claude", "skills", "daily-skill", "scripts", "run.sh"),
	}
	for _, path := range wantFiles {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected installed skill file %s: %v", path, err)
		}
	}
	if st, err := os.Stat(filepath.Join(targetRoot, ".claude", "skills", "daily-skill", "scripts", "run.sh")); err != nil {
		t.Fatalf("stat installed script: %v", err)
	} else if got := st.Mode().Perm(); got != 0o755 {
		t.Errorf("installed script mode = %#o, want 0755", got)
	}

	raw, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Installs []struct {
			Files      []string `yaml:"files"`
			FileClaims []struct {
				Path   string `yaml:"path"`
				SHA256 string `yaml:"sha256"`
			} `yaml:"file_claims"`
		} `yaml:"installs"`
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, raw)
	}
	if len(m.Installs) != 1 {
		t.Fatalf("manifest installs: got %d, want 1", len(m.Installs))
	}
	if got := len(m.Installs[0].Files); got != len(wantFiles) {
		t.Fatalf("manifest files: got %d, want %d; files=%v", got, len(wantFiles), m.Installs[0].Files)
	}
	claimed := map[string]string{}
	for _, claim := range m.Installs[0].FileClaims {
		claimed[claim.Path] = claim.SHA256
	}
	for _, path := range wantFiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read installed file %s: %v", path, err)
		}
		if got := claimed[path]; got != sha256ForTest(raw) {
			t.Errorf("manifest claim for %s = %q, want hash of installed bytes", path, got)
		}
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "daily-skill", "--agent", "claude-code", "--kind", "skill"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetRoot, ".claude", "skills", "daily-skill")); !os.IsNotExist(err) {
		t.Errorf("skill target directory should be removed after uninstall; stat=%v", err)
	}
}

func TestInventory_ReportsDriftedAndForeignUntrackedFileOutputs(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "daily-skill", "original\n")
	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", filepath.Join(agentsRoot, "skills", "daily-skill", "SKILL.md"), "--agent", "claude-code", "--scope", "project"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	installedSkill := filepath.Join(targetRoot, ".claude", "skills", "daily-skill", "SKILL.md")
	if err := os.WriteFile(installedSkill, []byte("user edit\n"), 0o644); err != nil {
		t.Fatalf("edit installed skill: %v", err)
	}
	rogueAgent := filepath.Join(targetRoot, ".claude", "agents", "rogue.md")
	if err := os.MkdirAll(filepath.Dir(rogueAgent), 0o755); err != nil {
		t.Fatalf("mkdir rogue: %v", err)
	}
	if err := os.WriteFile(rogueAgent, []byte("---\nname: rogue\ndescription: rogue\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write rogue: %v", err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"inventory", "--from", agentsRoot, "--target", targetRoot, "--agent", "claude-code"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inventory: %v\n%s", err, stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "drifted\t"+installedSkill) {
		t.Errorf("inventory should report drifted installed skill; got:\n%s", got)
	}
	if !strings.Contains(got, "foreign-untracked\t"+rogueAgent) {
		t.Errorf("inventory should report untracked rogue agent; got:\n%s", got)
	}
}

func TestResetMaterialized_RemovesTrackedAndIncludedUntrackedOutputs(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "daily-skill", "original\n")
	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", filepath.Join(agentsRoot, "skills", "daily-skill", "SKILL.md"), "--agent", "claude-code", "--scope", "project"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}
	tracked := filepath.Join(targetRoot, ".claude", "skills", "daily-skill", "SKILL.md")
	untracked := filepath.Join(targetRoot, ".claude", "rules", "manual.md")
	if err := os.MkdirAll(filepath.Dir(untracked), 0o755); err != nil {
		t.Fatalf("mkdir untracked: %v", err)
	}
	if err := os.WriteFile(untracked, []byte("---\nname: manual\n---\nmanual\n"), 0o644); err != nil {
		t.Fatalf("write untracked: %v", err)
	}

	var stdout bytes.Buffer
	reset := NewRootCmd()
	reset.SetOut(&stdout)
	reset.SetErr(&stdout)
	reset.SetArgs([]string{"reset-materialized", "--from", agentsRoot, "--target", targetRoot, "--include-untracked"})
	if err := reset.Execute(); err != nil {
		t.Fatalf("reset-materialized: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(tracked); !os.IsNotExist(err) {
		t.Errorf("tracked output should be removed; stat=%v", err)
	}
	if _, err := os.Stat(untracked); !os.IsNotExist(err) {
		t.Errorf("included untracked output should be removed; stat=%v", err)
	}
	records := readManifestRecords(t, dotpackHome)
	if len(records) != 0 {
		t.Fatalf("reset should remove manifest records; got %+v", records)
	}
}

func TestInstallAll_InstallsSupportedCanonicalResources(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "daily-skill", "original\n")
	rulesDir := filepath.Join(agentsRoot, "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("mkdir rules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "daily-rule.md"), []byte("---\nname: daily-rule\n---\nrule body\n"), 0o644); err != nil {
		t.Fatalf("write rule: %v", err)
	}

	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install-all", "--from", agentsRoot, "--target", targetRoot, "--agent", "claude-code", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install-all: %v\n%s", err, stdout.String())
	}
	for _, path := range []string{
		filepath.Join(targetRoot, ".claude", "skills", "daily-skill", "SKILL.md"),
		filepath.Join(targetRoot, ".claude", "rules", "daily-rule.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected install-all output %s: %v", path, err)
		}
	}
	records := readManifestRecords(t, dotpackHome)
	if len(records) != 2 {
		t.Fatalf("install-all should record two installs; got %+v", records)
	}
}

func TestInstallAll_CodexProjectScope_InstallsGraphifyRuleAndHookSidecar(t *testing.T) {
	sourceRoot := t.TempDir()
	mustWriteTestFile(
		t,
		filepath.Join(sourceRoot, ".agents", "rules", "graphify.md"),
		"---\nid: graphify\nname: graphify\n---\nrule body\n",
	)
	mustWriteTestFile(
		t,
		filepath.Join(sourceRoot, ".agents", "hooks", "graphify-context-codex.hook.json"),
		"{\"hooks\":{\"UserPromptSubmit\":[{\"matcher\":\"*\",\"hooks\":[{\"type\":\"command\",\"command\":\"bash \\\".agents/hooks/graphify-context.sh\\\"\",\"timeout\":10}]}]}}\n",
	)

	projectHome, dotpackHome := setupCodexProjectConfigEnv(t)

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install-all", "--from", sourceRoot, "--target", projectHome, "--agent", "codex", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install-all codex project scope: %v\n%s", err, stdout.String())
	}

	rulePath := filepath.Join(projectHome, ".codex", "rules", "graphify.md")
	if _, err := os.Stat(rulePath); err != nil {
		t.Fatalf("expected Codex rule at %s: %v", rulePath, err)
	}

	hooks := readCodexHooks(t, filepath.Join(projectHome, ".codex", "config.toml"))
	if len(hooks["UserPromptSubmit"]) != 1 {
		t.Fatalf("expected one UserPromptSubmit binding in Codex config; got %v", hooks)
	}
	specs, _ := hooks["UserPromptSubmit"][0]["hooks"].([]any)
	if len(specs) != 1 {
		t.Fatalf("expected one nested hook-spec; got %d", len(specs))
	}
	spec, _ := specs[0].(map[string]any)
	if spec["command"] != `bash ".agents/hooks/graphify-context.sh"` {
		t.Fatalf("unexpected Codex hook command: %v", spec)
	}

	records := readManifestRecords(t, dotpackHome)
	if len(records) != 2 {
		t.Fatalf("install-all should record two installs; got %+v", records)
	}
}

func TestInstallAll_KindPathOverridesDiscoverEverySupportedKind(t *testing.T) {
	sourceRoot := t.TempDir()
	writeCustomLayoutResources(t, sourceRoot)

	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install-all",
		"--from", sourceRoot,
		"--target", targetRoot,
		"--agent", "claude-code",
		"--scope", "project",
		"--kind-path", "skill=public-skills",
		"--kind-path", "agent=public-agents",
		"--kind-path", "rule=public-rules",
		"--kind-path", "command=public-commands",
		"--kind-path", "mcp-server=public-mcp",
		"--kind-path", "hook=public-hooks",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install-all: %v\n%s", err, stdout.String())
	}

	assertCustomLayoutOutputs(t, targetRoot)
	records := readManifestRecords(t, dotpackHome)
	if len(records) != 6 {
		t.Fatalf("install-all should record six installs; got %+v", records)
	}
}

func TestInstallAll_PerKindPathAliasesDiscoverEverySupportedKind(t *testing.T) {
	sourceRoot := t.TempDir()
	writeCustomLayoutResources(t, sourceRoot)

	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install-all",
		"--from", sourceRoot,
		"--target", targetRoot,
		"--agent", "claude-code",
		"--scope", "project",
		"--skills-path", "public-skills",
		"--agents-path", "public-agents",
		"--rules-path", "public-rules",
		"--commands-path", "public-commands",
		"--mcp-servers-path", "public-mcp",
		"--hooks-path", "public-hooks",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install-all: %v\n%s", err, stdout.String())
	}

	assertCustomLayoutOutputs(t, targetRoot)
	records := readManifestRecords(t, dotpackHome)
	if len(records) != 6 {
		t.Fatalf("install-all should record six installs; got %+v", records)
	}
}

func TestInstallAll_GitHubSourceCachesAndInstallsCustomSkillPath(t *testing.T) {
	sourceRepo := t.TempDir()
	mustWriteTestFile(t, filepath.Join(sourceRepo, "skills", "remote-skill", "SKILL.md"), "---\nname: remote-skill\ndescription: remote skill\n---\nremote body\n")
	restore := fakeGitCloneFrom(t, sourceRepo)
	defer restore()

	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install-all",
		"--from", "github:BuilderIO/skills@main",
		"--target", targetRoot,
		"--agent", "claude-code",
		"--scope", "project",
		"--skills-path", "skills",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install-all: %v\n%s", err, stdout.String())
	}

	installed := filepath.Join(targetRoot, ".claude", "skills", "remote-skill", "SKILL.md")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("expected remote skill installed at %s: %v", installed, err)
	}
	cached, err := filepath.Glob(filepath.Join(dotpackHome, "cache", "github", "BuilderIO", "skills", "*", "skills", "remote-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("glob cache: %v", err)
	}
	if len(cached) != 1 {
		t.Fatalf("expected one cached remote skill, got %v", cached)
	}
}

func TestInstallAll_GitHubSourceReusesCacheAndUpdatesExistingCheckout(t *testing.T) {
	sourceRepo := t.TempDir()
	mustWriteTestFile(t, filepath.Join(sourceRepo, "skills", "remote-skill", "SKILL.md"), "---\nname: remote-skill\ndescription: remote skill\n---\nremote body\n")
	stats, restore := fakeCountingGitCloneFrom(t, sourceRepo)
	defer restore()

	targetRoot := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	for i := 0; i < 2; i++ {
		var stdout bytes.Buffer
		cmd := NewRootCmd()
		cmd.SetOut(&stdout)
		cmd.SetErr(&stdout)
		cmd.SetArgs([]string{
			"install-all",
			"--from", "github:BuilderIO/skills",
			"--target", targetRoot,
			"--agent", "claude-code",
			"--scope", "project",
			"--skills-path", "skills",
		})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("install-all run %d: %v\n%s", i+1, err, stdout.String())
		}
	}

	if stats.cloneCalls != 1 {
		t.Fatalf("expected one clone, got %d", stats.cloneCalls)
	}
	if stats.updateCalls == 0 {
		t.Fatalf("expected existing cache to be updated on second install")
	}
}

func TestInstallAll_GitHubSourceRejectsMalformedSource(t *testing.T) {
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install-all",
		"--from", "github:BuilderIO",
		"--skills-path", "skills",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected malformed github source to fail")
	}
	if !strings.Contains(err.Error(), "github source") {
		t.Fatalf("error should mention github source, got %v", err)
	}
}

func TestSyncBack_CopiesUntrackedMaterializedFileIntoCanonical(t *testing.T) {
	configRoot := t.TempDir()
	agentsRoot := filepath.Join(configRoot, ".agents")
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatalf("mkdir agents root: %v", err)
	}
	targetRoot := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	materialized := filepath.Join(targetRoot, ".claude", "skills", "manual-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(materialized), 0o755); err != nil {
		t.Fatalf("mkdir materialized: %v", err)
	}
	raw := []byte("---\nname: manual-skill\ndescription: manual\n---\nmanual body\n")
	if err := os.WriteFile(materialized, raw, 0o644); err != nil {
		t.Fatalf("write materialized: %v", err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"sync-back", "--from", agentsRoot, "--target", targetRoot})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync-back: %v\n%s", err, stdout.String())
	}
	canonical := filepath.Join(agentsRoot, "skills", "manual-skill", "SKILL.md")
	got, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("read canonical: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("canonical bytes mismatch:\n%s", got)
	}
}

func TestSyncBack_CopiesSkillSupportFileIntoCanonicalRelativePath(t *testing.T) {
	configRoot := t.TempDir()
	agentsRoot := filepath.Join(configRoot, ".agents")
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatalf("mkdir agents root: %v", err)
	}
	targetRoot := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	materializedDir := filepath.Join(targetRoot, ".claude", "skills", "manual-skill")
	mustWriteTestFile(t, filepath.Join(materializedDir, "SKILL.md"), "---\nname: manual-skill\ndescription: manual\n---\nmanual body\n")
	refRaw := "# Guide\n\nSupport content.\n"
	mustWriteTestFile(t, filepath.Join(materializedDir, "references", "guide.md"), refRaw)

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"sync-back", "--from", agentsRoot, "--target", targetRoot})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync-back: %v\n%s", err, stdout.String())
	}

	canonicalRef := filepath.Join(agentsRoot, "skills", "manual-skill", "references", "guide.md")
	got, err := os.ReadFile(canonicalRef)
	if err != nil {
		t.Fatalf("read canonical reference: %v", err)
	}
	if string(got) != refRaw {
		t.Errorf("canonical reference bytes mismatch:\n%s", got)
	}
	skillRaw, err := os.ReadFile(filepath.Join(agentsRoot, "skills", "manual-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read canonical SKILL.md: %v", err)
	}
	if string(skillRaw) == refRaw {
		t.Errorf("support file was synced onto SKILL.md")
	}
}

func writeCanonicalSkill(t *testing.T, name, body string) (string, string) {
	t.Helper()
	configRoot := t.TempDir()
	agentsRoot := filepath.Join(configRoot, ".agents")
	skillDir := filepath.Join(agentsRoot, "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	raw := []byte("---\nname: " + name + "\ndescription: daily test\n---\n" + body)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), raw, 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	return configRoot, agentsRoot
}

func writeCustomLayoutResources(t *testing.T, root string) {
	t.Helper()
	mustWriteTestFile(t, filepath.Join(root, "public-skills", "custom-skill", "SKILL.md"), "---\nname: custom-skill\ndescription: custom skill\n---\nskill body\n")
	mustWriteTestFile(t, filepath.Join(root, "public-agents", "custom-agent.md"), "---\nname: custom-agent\ndescription: custom agent\n---\nagent body\n")
	mustWriteTestFile(t, filepath.Join(root, "public-rules", "custom-rule.md"), "---\nname: custom-rule\n---\nrule body\n")
	mustWriteTestFile(t, filepath.Join(root, "public-commands", "custom-command.md"), "---\nname: custom-command\ndescription: custom command\n---\ncommand body\n")
	mustWriteTestFile(t, filepath.Join(root, "public-mcp", "custom.mcp.json"), `{"mcpServers":{"layoutgithub":{"command":"npx","args":[]}}}`+"\n")
	mustWriteTestFile(t, filepath.Join(root, "public-hooks", "custom-hook.hook.json"), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/bin/true"}]}]}}`+"\n")
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type fakeGitStats struct {
	cloneCalls  int
	updateCalls int
}

func fakeGitCloneFrom(t *testing.T, sourceRepo string) func() {
	t.Helper()
	_, restore := fakeCountingGitCloneFrom(t, sourceRepo)
	return restore
}

func fakeCountingGitCloneFrom(t *testing.T, sourceRepo string) (*fakeGitStats, func()) {
	t.Helper()
	stats := &fakeGitStats{}
	previous := runGitCommand
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, nil
		}
		if args[0] == "clone" {
			stats.cloneCalls++
			dest := args[len(args)-1]
			if err := copyTestTree(sourceRepo, dest); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Join(dest, ".git"), 0o755); err != nil {
				return nil, err
			}
			return nil, nil
		}
		if args[0] == "-C" {
			stats.updateCalls++
			return nil, nil
		}
		return nil, nil
	}
	return stats, func() {
		runGitCommand = previous
	}
}

func copyTestTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	})
}

func assertCustomLayoutOutputs(t *testing.T, targetRoot string) {
	t.Helper()
	for _, path := range []string{
		filepath.Join(targetRoot, ".claude", "skills", "custom-skill", "SKILL.md"),
		filepath.Join(targetRoot, ".claude", "agents", "custom-agent.md"),
		filepath.Join(targetRoot, ".claude", "rules", "custom-rule.md"),
		filepath.Join(targetRoot, ".claude", "commands", "custom-command.md"),
		filepath.Join(targetRoot, ".mcp.json"),
		filepath.Join(targetRoot, ".claude", "settings.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected install-all output %s: %v", path, err)
		}
	}
}

func sha256ForTest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
