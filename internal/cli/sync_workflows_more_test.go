package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/dirs"
)

func TestRunInventoryNoOutputsAndErrorBranches(t *testing.T) {
	target := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", target)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runInventory(cmd, "", target, "claude-code", "project"); err != nil {
		t.Fatalf("runInventory empty: %v", err)
	}
	if !strings.Contains(out.String(), "no materialized") {
		t.Fatalf("runInventory empty output = %q", out.String())
	}
	if err := runInventory(cmd, "", target, "claude-code", "bad-scope"); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("runInventory bad scope err=%v", err)
	}
	if err := runInventory(cmd, filepath.Join(target, "missing"), target, "claude-code", "project"); err == nil || !strings.Contains(err.Error(), "stat --from") {
		t.Fatalf("runInventory bad from err=%v", err)
	}
}

func TestRunSyncBackSkipsKeepsAndErrorsOnDifferingCanonical(t *testing.T) {
	project := t.TempDir()
	agentsRoot := filepath.Join(t.TempDir(), ".agents")
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatalf("mkdir agentsRoot: %v", err)
	}
	t.Setenv("DOTPACK_PROJECT_HOME", project)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	mustWriteTestFile(t, filepath.Join(project, ".codex", "agents", "toml-agent.toml"), "name = 'toml-agent'\n")
	kept := filepath.Join(project, ".claude", "rules", "same.md")
	mustWriteTestFile(t, kept, "---\nname: same\n---\nsame\n")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "rules", "same.md"), "---\nname: same\n---\nsame\n")
	differing := filepath.Join(project, ".claude", "commands", "diff.md")
	mustWriteTestFile(t, differing, "---\ndescription: d\n---\nnew\n")
	mustWriteTestFile(t, filepath.Join(agentsRoot, "commands", "diff.md"), "old\n")

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err := runSyncBack(cmd, agentsRoot, project, false)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("runSyncBack differing canonical err=%v; want --force", err)
	}
	out.Reset()
	if err := runSyncBack(cmd, agentsRoot, project, true); err != nil {
		t.Fatalf("runSyncBack force: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "skipped") || !strings.Contains(got, "kept") || !strings.Contains(got, "synced") {
		t.Fatalf("sync-back output should include skipped/kept/synced, got:\n%s", got)
	}
}

func TestRunResetMaterializedNothingToReset(t *testing.T) {
	target := t.TempDir()
	t.Setenv("DOTPACK_PROJECT_HOME", target)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runResetMaterialized(cmd, "", target, false); err != nil {
		t.Fatalf("runResetMaterialized: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to reset") {
		t.Fatalf("reset output = %q", out.String())
	}
}

func TestResolveSourceLayoutRemoteAndErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	d := dirsForSyncLayoutTest(t)
	source := filepath.Join(tmp, ".agents")
	mustWriteTestFile(t, filepath.Join(source, "skills", "s", "SKILL.md"), "---\nname: s\ndescription: d\n---\nb\n")
	layout, err := resolveSourceLayout(tmp, sourceLayoutOptions{}, d)
	if err != nil || layout.root != source {
		t.Fatalf("resolveSourceLayout canonical = %+v err=%v", layout, err)
	}
	layout, err = resolveSourceLayout(tmp, sourceLayoutOptions{skillsPath: "custom-skills"}, d)
	if err != nil || layout.root != tmp || layout.paths["skill"] != "custom-skills" {
		t.Fatalf("resolveSourceLayout override = %+v err=%v", layout, err)
	}

	old := runGitCommand
	runGitCommand = func(workDir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "clone" {
			dest := args[len(args)-1]
			mustWriteTestFile(t, filepath.Join(dest, ".agents", "skills", "remote", "SKILL.md"), "---\nname: remote\ndescription: d\n---\nb\n")
			return nil, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { runGitCommand = old })
	layout, err = resolveSourceLayout("github:o/r", sourceLayoutOptions{}, d)
	if err != nil || !strings.Contains(layout.root, filepath.Join("cache", "github")) {
		t.Fatalf("resolveSourceLayout remote = %+v err=%v", layout, err)
	}
	runGitCommand = func(workDir string, args ...string) ([]byte, error) { return nil, errors.New("clone failed") }
	if _, err := resolveSourceLayout("github:o/fail", sourceLayoutOptions{}, d); err == nil || !strings.Contains(err.Error(), "clone") {
		t.Fatalf("resolveSourceLayout remote error=%v; want clone", err)
	}
}

func dirsForSyncLayoutTest(t *testing.T) dirs.Dirs {
	t.Helper()
	return dirs.Dirs{
		ClaudeHome:      t.TempDir(),
		GeminiHome:      t.TempDir(),
		AntigravityHome: t.TempDir(),
		AgentsHome:      t.TempDir(),
		CodexHome:       t.TempDir(),
		DotpackHome:     t.TempDir(),
		ProjectHome:     t.TempDir(),
	}
}
