package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/skillspector"
)

func TestInstallRunsMandatorySkillScanForSkills(t *testing.T) {
	configRoot, agentsRoot := writeCanonicalSkill(t, "scan-me", "body\n")
	dotpackHome := t.TempDir()
	claudeHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", configRoot)

	var commands []string
	var skills []string
	restore := stubMandatorySkillScan(t, func(command string, selection skillScanSelection, _ dirs.Dirs) error {
		commands = append(commands, command)
		for _, target := range selection.Targets {
			skills = append(skills, target.Name)
		}
		return nil
	})
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", filepath.Join(agentsRoot, "skills", "scan-me", "SKILL.md"), "--agent", "claude-code", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if len(commands) != 1 || commands[0] != "install" {
		t.Fatalf("mandatory skill scan commands = %v; want [install]", commands)
	}
	if len(skills) != 1 || skills[0] != "scan-me" {
		t.Fatalf("mandatory skill scan skills = %v; want [scan-me]", skills)
	}
}

func TestInstallMandatorySkillScanUsesAutomaticBaselineAndWritesAggregate(t *testing.T) {
	configRoot, agentsRoot := writeCanonicalSkill(t, "bad-skill", "body\n")
	dotpackHome := t.TempDir()
	claudeHome := t.TempDir()
	baselineDir := filepath.Join(configRoot, ".dotpack", "skillspector", "baselines")
	mustWriteTestFile(t, filepath.Join(baselineDir, "bad-skill.yaml"), "accepted_findings:\n  - id: SK001\n    reason: test baseline\n")
	prepareFakeSkillSpectorRuntime(t, dotpackHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", configRoot)

	restore := stubMandatorySkillScan(t, runMandatorySkillScan)
	defer restore()

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", filepath.Join(agentsRoot, "skills", "bad-skill", "SKILL.md"), "--agent", "claude-code", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install with automatic baseline: %v\n%s", err, stdout.String())
	}

	if !strings.Contains(stdout.String(), "Installed claude-code:skill:bad-skill") {
		t.Fatalf("install output missing success line:\n%s", stdout.String())
	}

	runEntries, err := os.ReadDir(filepath.Join(dotpackHome, "skillspector", "runs"))
	if err != nil {
		t.Fatalf("read skillspector runs: %v", err)
	}
	if len(runEntries) != 1 {
		t.Fatalf("run entries = %d; want 1", len(runEntries))
	}
	aggregatePath := filepath.Join(dotpackHome, "skillspector", "runs", runEntries[0].Name(), "mandatory-scan-aggregate.json")
	raw, err := os.ReadFile(aggregatePath)
	if err != nil {
		t.Fatalf("read aggregate output: %v", err)
	}
	for _, want := range []string{
		`"command": "install"`,
		`"skill": "bad-skill"`,
		`"issue_count": 0`,
		baselineDir,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("aggregate output missing %q:\n%s", want, string(raw))
		}
	}
}

func TestInstallReturnsLLMAgentPromptWhenRuntimeUnavailable(t *testing.T) {
	configRoot, agentsRoot := writeCanonicalSkill(t, "scan-me", "body\n")
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", configRoot)

	restoreScan := stubMandatorySkillScan(t, runMandatorySkillScan)
	defer restoreScan()
	restoreRuntime := stubEnsureSkillSpectorRuntime(t, func(string) (skillspector.Runtime, error) {
		return skillspector.Runtime{}, errors.New("create SkillSpector runtime: no compatible Python interpreter succeeded (python3: not found)\n\nPass this prompt to an LLM agent to install the managed SkillSpector dependency:\n\nPROMPT")
	})
	defer restoreRuntime()

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", filepath.Join(agentsRoot, "skills", "scan-me", "SKILL.md"), "--agent", "claude-code", "--scope", "user"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("install should fail when the SkillSpector runtime cannot be prepared")
	}
	for _, want := range []string{
		"install: ensure SkillSpector runtime:",
		"Pass this prompt to an LLM agent to install the managed SkillSpector dependency:",
		"PROMPT",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("install error missing %q:\n%s", want, err)
		}
	}
}

func TestInstallAllRunsMandatorySkillScanForSourceLayout(t *testing.T) {
	sourceProject, _ := writeCanonicalSkill(t, "batch-skill", "body\n")
	targetRoot := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)

	var commands []string
	var scanCount int
	restore := stubMandatorySkillScan(t, func(command string, selection skillScanSelection, _ dirs.Dirs) error {
		commands = append(commands, command)
		scanCount += len(selection.Targets)
		return nil
	})
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install-all", "--from", sourceProject, "--target", targetRoot, "--agent", "claude-code", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install-all: %v", err)
	}

	if len(commands) != 1 || commands[0] != "install-all" {
		t.Fatalf("mandatory skill scan commands = %v; want [install-all]", commands)
	}
	if scanCount != 1 {
		t.Fatalf("mandatory skill scan count = %d; want 1", scanCount)
	}
}

func TestInventoryRunsMandatorySkillScanForCanonicalSkills(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "inventory-skill", "body\n")
	targetRoot := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)

	var commands []string
	restore := stubMandatorySkillScan(t, func(command string, selection skillScanSelection, _ dirs.Dirs) error {
		commands = append(commands, command)
		if len(selection.Targets) != 1 || selection.Targets[0].Name != "inventory-skill" {
			t.Fatalf("inventory selection = %+v", selection)
		}
		return nil
	})
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"inventory", "--from", agentsRoot, "--target", targetRoot, "--agent", "claude-code", "--scope", "project"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inventory: %v", err)
	}

	if len(commands) != 1 || commands[0] != "inventory" {
		t.Fatalf("mandatory skill scan commands = %v; want [inventory]", commands)
	}
}

func TestImportRunsMandatorySkillScanForClaudeSkills(t *testing.T) {
	srcProject := t.TempDir()
	claudeRoot := filepath.Join(srcProject, ".claude")
	outProject := t.TempDir()
	mustWrite(t, filepath.Join(claudeRoot, "skills", "demo", "SKILL.md"), "---\nname: demo\ndescription: demo\n---\nbody\n")
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", outProject)

	var commands []string
	restore := stubMandatorySkillScan(t, func(command string, selection skillScanSelection, _ dirs.Dirs) error {
		commands = append(commands, command)
		if len(selection.Targets) != 1 || selection.Targets[0].Name != "demo" {
			t.Fatalf("import selection = %+v", selection)
		}
		return nil
	})
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"import", "claude-code", srcProject, "--out", outProject})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import: %v", err)
	}

	if len(commands) != 1 || commands[0] != "import" {
		t.Fatalf("mandatory skill scan commands = %v; want [import]", commands)
	}
}

func TestSyncBackRunsMandatorySkillScanForMaterializedSkills(t *testing.T) {
	configRoot := t.TempDir()
	agentsRoot := filepath.Join(configRoot, ".agents")
	targetRoot := t.TempDir()
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatalf("mkdir agents root: %v", err)
	}
	mustWriteTestFile(t, filepath.Join(targetRoot, ".claude", "skills", "demo", "SKILL.md"), "---\nname: demo\ndescription: demo\n---\nbody\n")
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)

	var commands []string
	restore := stubMandatorySkillScan(t, func(command string, selection skillScanSelection, _ dirs.Dirs) error {
		commands = append(commands, command)
		if len(selection.Targets) != 1 || selection.Targets[0].Name != "demo" {
			t.Fatalf("sync-back selection = %+v", selection)
		}
		return nil
	})
	defer restore()

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"sync-back", "--from", agentsRoot, "--target", targetRoot})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync-back: %v", err)
	}

	if len(commands) != 1 || commands[0] != "sync-back" {
		t.Fatalf("mandatory skill scan commands = %v; want [sync-back]", commands)
	}
}

func stubMandatorySkillScan(t *testing.T, fn func(string, skillScanSelection, dirs.Dirs) error) func() {
	t.Helper()
	previous := mandatorySkillScan
	mandatorySkillScan = fn
	return func() {
		mandatorySkillScan = previous
	}
}

func stubEnsureSkillSpectorRuntime(t *testing.T, fn func(string) (skillspector.Runtime, error)) func() {
	t.Helper()
	previous := ensureSkillSpectorRuntime
	ensureSkillSpectorRuntime = fn
	return func() {
		ensureSkillSpectorRuntime = previous
	}
}
