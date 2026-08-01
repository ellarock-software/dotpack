package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCommandRunner struct {
	lookPathResults map[string][]lookPathResult
	runErrs         map[string]error
	runs            []string
}

type lookPathResult struct {
	path string
	err  error
}

func (f *fakeCommandRunner) LookPath(file string) (string, error) {
	results := f.lookPathResults[file]
	if len(results) == 0 {
		return "", exec.ErrNotFound
	}
	next := results[0]
	f.lookPathResults[file] = results[1:]
	return next.path, next.err
}

func (f *fakeCommandRunner) Run(name string, args ...string) error {
	call := name + " " + strings.Join(args, " ")
	f.runs = append(f.runs, call)
	if err, ok := f.runErrs[call]; ok {
		return err
	}
	return nil
}

func withFakeLifecycleRunner(t *testing.T, runner *fakeCommandRunner) {
	t.Helper()
	orig := lifecycleRunner
	lifecycleRunner = runner
	t.Cleanup(func() { lifecycleRunner = orig })
}

func withPostInstallLifecycle(t *testing.T, fn func(string) error) {
	t.Helper()
	orig := runPostInstallLifecycle
	runPostInstallLifecycle = fn
	t.Cleanup(func() { runPostInstallLifecycle = orig })
}

func TestLifecycleMetadataDeclaresNoBundledTasks(t *testing.T) {
	def, err := loadLifecycleDefinition()
	if err != nil {
		t.Fatalf("load lifecycle definition: %v", err)
	}
	if len(def.Tasks) != 0 {
		t.Fatalf("tasks = %d; want 0", len(def.Tasks))
	}
}

func TestLifecycleNoopsForUnrelatedHosts(t *testing.T) {
	runner := &fakeCommandRunner{}
	withFakeLifecycleRunner(t, runner)

	if err := runLifecyclePhase(lifecyclePhasePostInstall, "claude-code"); err != nil {
		t.Fatalf("claude-code lifecycle should be a no-op: %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("expected no lifecycle commands for claude-code, got %v", runner.runs)
	}
}

func TestLifecycleTaskRunsConfiguredBinary(t *testing.T) {
	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"example-guard": {{path: "/usr/local/bin/example-guard"}},
		},
	}
	withFakeLifecycleRunner(t, runner)

	task := lifecycleTask{
		Name:   "example",
		Ensure: lifecycleEnsure{Binaries: []lifecycleBinary{{Name: "example-guard"}}},
		Run:    []lifecycleCommand{{Command: "example-guard", Args: []string{"install"}}},
		Verify: []lifecycleCommand{{Command: "example-guard", Args: []string{"status"}}},
	}
	if err := runLifecycleTask(task); err != nil {
		t.Fatalf("lifecycle: %v", err)
	}

	want := []string{
		"/usr/local/bin/example-guard install",
		"/usr/local/bin/example-guard status",
	}
	if fmt.Sprint(runner.runs) != fmt.Sprint(want) {
		t.Fatalf("runs = %v; want %v", runner.runs, want)
	}
}

func TestLifecycleFailsWhenRequiredBinaryIsMissing(t *testing.T) {
	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"example-guard": {{err: exec.ErrNotFound}},
		},
	}
	withFakeLifecycleRunner(t, runner)

	err := runLifecycleTask(lifecycleTask{
		Name:   "example",
		Ensure: lifecycleEnsure{Binaries: []lifecycleBinary{{Name: "example-guard"}}},
	})
	if err == nil {
		t.Fatal("expected lifecycle failure when the required binary is missing")
	}
	if !strings.Contains(err.Error(), "no install candidates are declared") {
		t.Fatalf("error should explain that the binary must be installed separately; got %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("missing binary must not trigger installer commands; got %v", runner.runs)
	}
}

func TestInstallCodexTriggersPostInstallLifecycleWhenFlagIsSet(t *testing.T) {
	agentsHome, _ := setupCodexEnv(t)
	var called []string
	withPostInstallLifecycle(t, func(agent string) error {
		called = append(called, agent)
		return nil
	})

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--scope", "user", "--run-lifecycle"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install codex: %v\n%s", err, stdout.String())
	}

	target := filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if called == nil || len(called) != 1 || called[0] != "codex" {
		t.Fatalf("post-install lifecycle calls = %v; want [codex]", called)
	}
	if !strings.Contains(stdout.String(), "Installed codex:skill:dotpack-tracer-bullet") {
		t.Fatalf("install success output missing after lifecycle; got %q", stdout.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected installed skill at %s: %v", target, err)
	}
}

func TestInstallReportsMaterializedButLifecycleFailed(t *testing.T) {
	agentsHome, _ := setupCodexEnv(t)
	withPostInstallLifecycle(t, func(agent string) error {
		return errors.New("unknown host codex")
	})

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--scope", "user", "--run-lifecycle"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected install error when mandatory lifecycle fails")
	}
	for _, want := range []string{"installed codex:skill:dotpack-tracer-bullet", "post-install lifecycle failed", "unknown host codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(agentsHome, "skills", "dotpack-tracer-bullet", "SKILL.md")); statErr != nil {
		t.Fatalf("materialization should have happened before lifecycle failure is reported: %v", statErr)
	}
}

func TestInstallDoesNotRunLifecycleByDefault(t *testing.T) {
	claudeHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	var called []string
	withPostInstallLifecycle(t, func(agent string) error {
		called = append(called, agent)
		return nil
	})

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install claude-code: %v", err)
	}
	if len(called) != 0 {
		t.Fatalf("post-install lifecycle should be opt-in; got calls %v", called)
	}
}
