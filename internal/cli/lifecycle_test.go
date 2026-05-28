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
	t.Setenv("DOTPACK_SPONSIO_BINARY", "")
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

func TestLifecycleMetadataDeclaresSponsioAsData(t *testing.T) {
	def, err := loadLifecycleDefinition()
	if err != nil {
		t.Fatalf("load lifecycle definition: %v", err)
	}
	if len(def.Tasks) != 1 {
		t.Fatalf("tasks = %d; want 1", len(def.Tasks))
	}
	task := def.Tasks[0]
	if task.Name != "sponsio-enforcement" {
		t.Fatalf("task name = %q; want sponsio-enforcement", task.Name)
	}
	if task.Phase != lifecyclePhasePostInstall {
		t.Fatalf("phase = %q; want %q", task.Phase, lifecyclePhasePostInstall)
	}
	if fmt.Sprint(task.AppliesTo.Agents) != fmt.Sprint([]string{"codex", "gemini-cli", "antigravity-cli", "agents-cli"}) {
		t.Fatalf("agents = %v", task.AppliesTo.Agents)
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

func TestLifecycleExistingBinaryRunsInstallAndVerifiesRequiredHosts(t *testing.T) {
	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"sponsio": {{path: "/usr/local/bin/sponsio"}},
		},
	}
	withFakeLifecycleRunner(t, runner)

	if err := runLifecyclePhase(lifecyclePhasePostInstall, "codex"); err != nil {
		t.Fatalf("lifecycle: %v", err)
	}

	want := []string{
		"/usr/local/bin/sponsio host install all --mode enforce",
		"/usr/local/bin/sponsio host status codex",
		"/usr/local/bin/sponsio host status gemini-cli",
		"/usr/local/bin/sponsio host status antigravity-cli",
	}
	if fmt.Sprint(runner.runs) != fmt.Sprint(want) {
		t.Fatalf("runs = %v; want %v", runner.runs, want)
	}
}

func TestLifecycleInstallsWhenMissingThenRunsEnforcement(t *testing.T) {
	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"sponsio": {
				{err: exec.ErrNotFound},
				{path: "/opt/homebrew/bin/sponsio"},
			},
			"pip": {{path: "/opt/homebrew/bin/pip"}},
		},
	}
	withFakeLifecycleRunner(t, runner)

	if err := runLifecyclePhase(lifecyclePhasePostInstall, "agents-cli"); err != nil {
		t.Fatalf("lifecycle: %v", err)
	}

	wantPrefix := []string{
		"/opt/homebrew/bin/pip install sponsio",
		"/opt/homebrew/bin/sponsio host install all --mode enforce",
	}
	for i, want := range wantPrefix {
		if runner.runs[i] != want {
			t.Fatalf("runs[%d] = %q; want %q (all runs: %v)", i, runner.runs[i], want, runner.runs)
		}
	}
}

func TestLifecycleFailsClosedWhenInstallersMissing(t *testing.T) {
	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"sponsio": {{err: exec.ErrNotFound}},
			"pip":     {{err: exec.ErrNotFound}},
			"pip3":    {{err: exec.ErrNotFound}},
		},
	}
	withFakeLifecycleRunner(t, runner)

	err := runLifecyclePhase(lifecyclePhasePostInstall, "gemini-cli")
	if err == nil {
		t.Fatal("expected lifecycle failure when Sponsio and installers are missing")
	}
	if !strings.Contains(err.Error(), "no installer candidate succeeded") {
		t.Fatalf("error should explain installer failure; got %v", err)
	}
}

func TestLifecycleFailsClosedWhenRequiredHostIsUnsupported(t *testing.T) {
	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"sponsio": {{path: "/usr/local/bin/sponsio"}},
		},
		runErrs: map[string]error{
			"/usr/local/bin/sponsio host status codex": errors.New("unknown host codex"),
		},
	}
	withFakeLifecycleRunner(t, runner)

	err := runLifecyclePhase(lifecyclePhasePostInstall, "codex")
	if err == nil {
		t.Fatal("expected lifecycle failure when Sponsio lacks codex support")
	}
	if !strings.Contains(err.Error(), "verify sponsio host status codex") {
		t.Fatalf("error should name the failed verify command; got %v", err)
	}
}

func TestInstallCodexTriggersPostInstallLifecycle(t *testing.T) {
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
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--scope", "user"})
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
	cmd.SetArgs([]string{"install", src, "--agent", "codex", "--scope", "user"})
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

func TestInstallClaudeCodeStillRunsLifecycleExtensionPoint(t *testing.T) {
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
	if fmt.Sprint(called) != fmt.Sprint([]string{"claude-code"}) {
		t.Fatalf("post-install lifecycle should be invoked for every install; got %v", called)
	}
}
