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
	"github.com/spf13/cobra"
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

func TestSkillBearingCommandsExposeSecurityBypassFlag(t *testing.T) {
	commands := []*cobra.Command{
		newInstallCmd(),
		newInstallAllCmd(),
		newInventoryCmd(),
		newImportCmd(),
		newSyncBackCmd(),
		newScanSkillsCmd(),
	}
	for _, cmd := range commands {
		if flag := cmd.Flags().Lookup(skillSecurityBypassFlag); flag == nil {
			t.Errorf("%s command is missing --%s", cmd.Name(), skillSecurityBypassFlag)
		}
	}
	if flag := newBaselineSkillsCmd().Flags().Lookup(skillSecurityBypassFlag); flag != nil {
		t.Errorf("baseline-skills must not expose --%s", skillSecurityBypassFlag)
	}
}

func TestInstallSecurityBypassFiltersMandatoryScanAndReportsWarning(t *testing.T) {
	configRoot, agentsRoot := writeCanonicalSkill(t, "scan-me", "body\n")
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", configRoot)

	var scanned skillScanSelection
	restore := stubMandatorySkillScan(t, func(_ string, selection skillScanSelection, _ dirs.Dirs) error {
		scanned = selection
		return nil
	})
	defer restore()

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"install",
		filepath.Join(agentsRoot, "skills", "scan-me", "SKILL.md"),
		"--agent", "claude-code",
		"--scope", "user",
		"--skill-bypass-security", "scan-me",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install with security bypass: %v\n%s", err, stdout.String())
	}
	if len(scanned.Targets) != 0 || len(scanned.SecurityBypassed) != 1 || scanned.SecurityBypassed[0].Name != "scan-me" {
		t.Fatalf("mandatory scan selection = %+v", scanned)
	}
	if !strings.Contains(stdout.String(), `SECURITY BYPASS: SkillSpector skipped skill "scan-me"`) {
		t.Fatalf("install output missing security warning:\n%s", stdout.String())
	}
}

func TestAutomaticGateCommandsPassSecurityBypassThroughCentralPolicy(t *testing.T) {
	t.Run("install-all", func(t *testing.T) {
		sourceProject, _ := writeCanonicalSkill(t, "bypass-me", "body\n")
		targetRoot := t.TempDir()
		t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
		t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)

		executeCommandExpectingSecurityBypass(t, []string{
			"install-all",
			"--from", sourceProject,
			"--target", targetRoot,
			"--agent", "claude-code",
			"--scope", "project",
			"--skill-bypass-security", "bypass-me",
		}, "install-all", "bypass-me")
	})

	t.Run("inventory", func(t *testing.T) {
		_, agentsRoot := writeCanonicalSkill(t, "bypass-me", "body\n")
		targetRoot := t.TempDir()
		t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
		t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)

		executeCommandExpectingSecurityBypass(t, []string{
			"inventory",
			"--from", agentsRoot,
			"--target", targetRoot,
			"--agent", "claude-code",
			"--scope", "project",
			"--skill-bypass-security", "bypass-me",
		}, "inventory", "bypass-me")
	})

	t.Run("import", func(t *testing.T) {
		srcProject := t.TempDir()
		outProject := t.TempDir()
		mustWrite(t, filepath.Join(srcProject, ".claude", "skills", "bypass-me", "SKILL.md"), "---\nname: bypass-me\ndescription: demo\n---\nbody\n")
		t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
		t.Setenv("DOTPACK_PROJECT_HOME", outProject)

		executeCommandExpectingSecurityBypass(t, []string{
			"import",
			"claude-code",
			srcProject,
			"--out", outProject,
			"--skill-bypass-security", "bypass-me",
		}, "import", "bypass-me")
	})

	t.Run("sync-back", func(t *testing.T) {
		configRoot := t.TempDir()
		agentsRoot := filepath.Join(configRoot, ".agents")
		targetRoot := t.TempDir()
		if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
			t.Fatalf("mkdir agents root: %v", err)
		}
		mustWriteTestFile(t, filepath.Join(targetRoot, ".claude", "skills", "bypass-me", "SKILL.md"), "---\nname: bypass-me\ndescription: demo\n---\nbody\n")
		t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
		t.Setenv("DOTPACK_PROJECT_HOME", targetRoot)

		executeCommandExpectingSecurityBypass(t, []string{
			"sync-back",
			"--from", agentsRoot,
			"--target", targetRoot,
			"--skill-bypass-security", "bypass-me",
		}, "sync-back", "bypass-me")
	})
}

func executeCommandExpectingSecurityBypass(t *testing.T, args []string, command, skillName string) {
	t.Helper()
	var scanned skillScanSelection
	var scannedCommand string
	restore := stubMandatorySkillScan(t, func(gotCommand string, selection skillScanSelection, _ dirs.Dirs) error {
		scannedCommand = gotCommand
		scanned = selection
		return nil
	})
	defer restore()

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%s with security bypass: %v\n%s", command, err, stdout.String())
	}
	if scannedCommand != command {
		t.Fatalf("mandatory scan command = %q; want %q", scannedCommand, command)
	}
	if len(scanned.Targets) != 0 || len(scanned.SecurityBypassed) != 1 || scanned.SecurityBypassed[0].Name != skillName {
		t.Fatalf("mandatory scan selection = %+v", scanned)
	}
	if !strings.Contains(stdout.String(), `SECURITY BYPASS: SkillSpector skipped skill "`+skillName+`"`) {
		t.Fatalf("%s output missing security warning:\n%s", command, stdout.String())
	}
}

func TestInstallRejectsSecurityBypassForNonSkillResource(t *testing.T) {
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install",
		filepath.Join(t.TempDir(), "AGENTS.md"),
		"--kind", "memory",
		"--skill-bypass-security", "not-a-skill",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--skill-bypass-security is only valid when installing a skill") {
		t.Fatalf("non-skill bypass err=%v; want explicit rejection", err)
	}
}

func TestInstallUnknownSecurityBypassFailsBeforeMaterialization(t *testing.T) {
	configRoot, agentsRoot := writeCanonicalSkill(t, "scan-me", "body\n")
	claudeHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_PROJECT_HOME", configRoot)

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{
		"install",
		filepath.Join(agentsRoot, "skills", "scan-me", "SKILL.md"),
		"--agent", "claude-code",
		"--scope", "user",
		"--skill-bypass-security", "missing",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "skill security bypass name(s) not selected: missing") {
		t.Fatalf("unknown bypass err=%v; want fail-closed selection error", err)
	}
	if pathIsRegularFile(filepath.Join(claudeHome, "skills", "scan-me", "SKILL.md")) {
		t.Fatal("unknown security bypass materialized a skill before failing")
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

func TestResolveAutomaticBaselineDirFindsCanonicalAgentGateBaselines(t *testing.T) {
	configRoot := t.TempDir()
	baselineDir := filepath.Join(configRoot, ".agents", "tools", "skillspector-gate", "baselines")
	mustWriteTestFile(t, filepath.Join(baselineDir, "safe-skill.yaml"), "accepted_findings: []\n")

	got, err := resolveAutomaticBaselineDir(filepath.Join(configRoot, ".agents"))
	if err != nil {
		t.Fatalf("resolve automatic baseline dir: %v", err)
	}
	if got != baselineDir {
		t.Fatalf("baseline dir = %q; want %q", got, baselineDir)
	}
}

func TestMandatorySkillScanAllowsPartialReviewedBaselines(t *testing.T) {
	configRoot, agentsRoot := writeCanonicalSkill(t, "bad-skill", "body\n")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "skills", "good-skill", "SKILL.md"), "---\nname: good-skill\ndescription: good\n---\nbody\n")
	baselineDir := filepath.Join(configRoot, ".agents", "tools", "skillspector-gate", "baselines")
	mustWriteTestFile(t, filepath.Join(baselineDir, "bad-skill.yaml"), "accepted_findings:\n  - id: SK001\n    reason: reviewed\n")
	dotpackHome := t.TempDir()
	prepareFakeSkillSpectorRuntime(t, dotpackHome)
	runtime, err := skillspector.EnsureRuntime(dotpackHome)
	if err != nil {
		t.Fatalf("ensure fake runtime: %v", err)
	}
	selection, err := buildSkillScanSelection(agentsRoot, filepath.Join(agentsRoot, "skills"))
	if err != nil {
		t.Fatalf("build selection: %v", err)
	}

	results, _, err := runSkillScansWithOptionalBaselines(selection.Targets, t.TempDir(), baselineDir, "json", runtime)
	if err != nil {
		t.Fatalf("scan with partial baselines: %v", err)
	}
	if len(results) != 2 || results[0].BaselinePath == "" || results[1].BaselinePath != "" {
		t.Fatalf("partial baseline results = %+v", results)
	}
}

func TestMandatorySkillScanAllSecurityBypassedWritesAggregateWithoutRuntime(t *testing.T) {
	_, agentsRoot := writeCanonicalSkill(t, "bypass-me", "body\n")
	dotpackHome := t.TempDir()
	selection := skillScanSelection{
		SourceRoot: agentsRoot,
		SkillRoot:  filepath.Join(agentsRoot, "skills"),
		SecurityBypassed: []skillScanTarget{{
			Name:         "bypass-me",
			SkillDir:     filepath.Join(agentsRoot, "skills", "bypass-me"),
			SkillFile:    filepath.Join(agentsRoot, "skills", "bypass-me", "SKILL.md"),
			RelativePath: "skills/bypass-me",
		}},
	}
	restore := stubEnsureSkillSpectorRuntime(t, func(string) (skillspector.Runtime, error) {
		return skillspector.Runtime{}, errors.New("runtime provisioning must not run")
	})
	defer restore()

	if err := runMandatorySkillScan("install", selection, dirs.Dirs{DotpackHome: dotpackHome}); err != nil {
		t.Fatalf("all-bypassed mandatory scan: %v", err)
	}
	runEntries, err := os.ReadDir(filepath.Join(dotpackHome, "skillspector", "runs"))
	if err != nil {
		t.Fatalf("read skillspector runs: %v", err)
	}
	if len(runEntries) != 1 {
		t.Fatalf("run entries = %d; want 1", len(runEntries))
	}
	raw, err := os.ReadFile(filepath.Join(dotpackHome, "skillspector", "runs", runEntries[0].Name(), "mandatory-scan-aggregate.json"))
	if err != nil {
		t.Fatalf("read mandatory aggregate: %v", err)
	}
	for _, want := range []string{`"skills_scanned": 0`, `"security_bypassed_skills"`, `"name": "bypass-me"`, `"pass": true`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("mandatory aggregate missing %q:\n%s", want, string(raw))
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
