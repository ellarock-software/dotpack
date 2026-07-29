package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/orchestrator"
	"github.com/ellarock-software/dotpack/internal/resource"
)

func TestLifecycleAdditionalBranches(t *testing.T) {
	if err := runPostInstallLifecycle("claude-code"); err != nil {
		t.Fatalf("default post-install lifecycle should no-op for claude-code: %v", err)
	}
	if err := (execCommandRunner{}).Run("sh", "-c", "exit 0"); err != nil {
		t.Fatalf("exec runner success: %v", err)
	}
	if err := (execCommandRunner{}).Run("sh", "-c", "echo boom; exit 1"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("exec runner output error=%v; want boom", err)
	}
	if err := (execCommandRunner{}).Run("sh", "-c", "exit 1"); err == nil {
		t.Fatal("exec runner no-output failure expected error")
	}

	oldConfig := defaultLifecycleConfig
	defaultLifecycleConfig = []byte("version: 2\n")
	if _, err := loadLifecycleDefinition(); err == nil || !strings.Contains(err.Error(), "unsupported lifecycle") {
		t.Fatalf("lifecycle version err=%v; want unsupported lifecycle", err)
	}
	defaultLifecycleConfig = []byte("[")
	if err := runLifecyclePhase(lifecyclePhasePostInstall, "agents-cli"); err == nil || !strings.Contains(err.Error(), "load lifecycle tasks") {
		t.Fatalf("lifecycle load err=%v; want load lifecycle tasks", err)
	}
	defaultLifecycleConfig = oldConfig

	if (lifecycleAppliesTo{}).matches("phase", "agent", "other") {
		t.Fatal("matches should reject different phase")
	}
	if !(lifecycleAppliesTo{}).matches("phase", "agent", "phase") {
		t.Fatal("matches should accept empty agent list")
	}
	if (lifecycleTask{}).failurePolicy() != "fail-closed" {
		t.Fatal("empty failure policy should be fail-closed")
	}
	if formatLifecycleCommand(lifecycleCommand{Command: "tool"}) != "tool" {
		t.Fatal("formatLifecycleCommand without args should return command")
	}

	runner := &fakeCommandRunner{
		lookPathResults: map[string][]lookPathResult{
			"tool":      {{err: exec.ErrNotFound}, {err: errors.New("path lookup failed")}},
			"installer": {{path: "/bin/installer"}},
		},
	}
	withFakeLifecycleRunner(t, runner)
	_, err := ensureLifecycleBinary(lifecycleBinary{Name: "tool", Install: lifecycleInstaller{Candidates: []lifecycleCommand{{Command: "installer"}}}})
	if err == nil || !strings.Contains(err.Error(), "after installer install") {
		t.Fatalf("ensure after-install lookup err=%v; want after installer install", err)
	}

	runner = &fakeCommandRunner{runErrs: map[string]error{"missing ": errors.New("run failed")}}
	withFakeLifecycleRunner(t, runner)
	if err := runLifecycleTask(lifecycleTask{Run: []lifecycleCommand{{Command: "missing"}}}); err == nil || !strings.Contains(err.Error(), "run missing") {
		t.Fatalf("runLifecycleTask run err=%v; want run missing", err)
	}
}

func TestImportRunAndAdditionalErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "project")
	claude := filepath.Join(project, ".claude")
	mustWrite(t, filepath.Join(claude, "CLAUDE.md"), "hello .claude\n")
	cmd, out := newBufferedTestCmd()
	if err := runImport(cmd, "claude-code", project, filepath.Join(tmp, "out"), false); err != nil {
		t.Fatalf("runImport success: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Imported claude-code") || !strings.Contains(got, "agents.md") {
		t.Fatalf("runImport output = %q", got)
	}
	if _, err := resolveClaudeImportRoot(claude); err != nil {
		t.Fatalf("resolve direct .claude: %v", err)
	}
	if err := runImport(cmd, "unknown", project, filepath.Join(tmp, "out2"), false); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("runImport unsupported err=%v; want not supported", err)
	}
	if err := runImport(cmd, "claude-code", filepath.Join(tmp, "missing"), filepath.Join(tmp, "out3"), false); err == nil || !strings.Contains(err.Error(), "source path") {
		t.Fatalf("runImport missing source err=%v; want source path", err)
	}

	imp := claudeImporter{claudeRoot: filepath.Join(tmp, "err", ".claude"), agentsRoot: filepath.Join(tmp, "err", ".agents"), written: map[string]struct{}{}}
	if err := imp.copyTree(filepath.Join(tmp, "missing"), filepath.Join(tmp, "dst")); err == nil || !strings.Contains(err.Error(), "stat") {
		t.Fatalf("copyTree missing err=%v; want stat", err)
	}
	dsStore := filepath.Join(tmp, ".DS_Store")
	if err := imp.copyFile(dsStore, filepath.Join(tmp, "ignored"), 0o644); err != nil {
		t.Fatalf("copyFile should skip .DS_Store before read: %v", err)
	}
	src := filepath.Join(tmp, "src.md")
	dst := filepath.Join(tmp, "dst.md")
	mustWrite(t, src, "x")
	mustWrite(t, dst, "old")
	if err := imp.copyFile(src, dst, 0o644); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("copyFile overwrite err=%v; want refusing", err)
	}

	settingsDir := filepath.Join(tmp, "settings-dir", ".claude", "settings.json")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir settings dir: %v", err)
	}
	imp = claudeImporter{claudeRoot: filepath.Dir(settingsDir), agentsRoot: filepath.Join(tmp, "settings-dir", ".agents"), written: map[string]struct{}{}}
	if err := imp.importSettings(); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("importSettings dir err=%v; want read", err)
	}

	imp = claudeImporter{claudeRoot: claude, agentsRoot: filepath.Join(tmp, "stat-target", ".agents"), written: map[string]struct{}{}}
	parentFile := filepath.Join(imp.agentsRoot, "parent")
	mustWrite(t, parentFile, "x")
	if err := imp.writeGeneratedJSON(filepath.Join(parentFile, "child.json"), []byte("{}")); err == nil || !strings.Contains(err.Error(), "stat target") {
		t.Fatalf("writeGeneratedJSON stat err=%v; want stat target", err)
	}
}

func TestPruneReconcileListAndUninstallCLIBranches(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	cmd, out := newBufferedTestCmd()
	if err := runReconcile(cmd); err != nil {
		t.Fatalf("runReconcile empty: %v", err)
	}
	if !strings.Contains(out.String(), "no installs") {
		t.Fatalf("runReconcile empty output = %q", out.String())
	}

	store := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	present := filepath.Join(d.ProjectHome, "present.md")
	mustWriteTestFile(t, present, "changed")
	missing := filepath.Join(d.ProjectHome, "missing.md")
	records := []manifest.Record{
		{ID: "host:skill:present", Scope: string(adapter.ScopeProject), TargetRoot: d.ProjectHome, Files: []string{present}, FileClaims: []manifest.FileClaim{{Path: present, SHA256: sha256String([]byte("original"))}}},
		{ID: "host:skill:missing", Scope: string(adapter.ScopeProject), TargetRoot: d.ProjectHome, Files: []string{missing}, TargetDir: filepath.Join(d.ProjectHome, "empty-dir")},
	}
	if err := os.MkdirAll(records[1].TargetDir, 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	for _, rec := range records {
		if err := store.Upsert(rec); err != nil {
			t.Fatalf("Upsert(%s): %v", rec.ID, err)
		}
	}
	cmd, out = newBufferedTestCmd()
	if err := runReconcile(cmd); err != nil {
		t.Fatalf("runReconcile drift: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "drifted file") || !strings.Contains(got, "missing file") {
		t.Fatalf("runReconcile drift output:\n%s", got)
	}
	cmd, out = newBufferedTestCmd()
	if err := runPrune(cmd); err != nil {
		t.Fatalf("runPrune: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Pruned host:skill:missing") || !strings.Contains(got, "kept 1 partially present install") {
		t.Fatalf("runPrune output:\n%s", got)
	}

	badHome := t.TempDir()
	t.Setenv("DOTPACK_DOTPACK_HOME", badHome)
	if err := os.WriteFile(filepath.Join(badHome, "installs.yaml"), []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	if err := runList(&cobra.Command{}); err == nil {
		t.Fatal("runList should return manifest parse error")
	}
	if err := runPrune(&cobra.Command{}); err == nil {
		t.Fatal("runPrune should return manifest parse error")
	}
	if err := runUninstall(&cobra.Command{}, "", "", "skill", ""); err == nil || !strings.Contains(err.Error(), "provide either") {
		t.Fatalf("runUninstall resolve err=%v; want provide either", err)
	}

	var rendered bytes.Buffer
	renderCmd := &cobra.Command{}
	renderCmd.SetOut(&rendered)
	printReconcileStatus(renderCmd, orchestrator.ReconcileStatus{
		Record:            manifest.Record{ID: "id"},
		MissingFiles:      []string{"missing"},
		DriftedFiles:      []orchestrator.FileDrift{{Path: "drift", ExpectedSHA256: "a", ActualSHA256: "b"}},
		MissingMergedKeys: []manifest.MergedKey{{File: "config.json", Path: "$.x"}},
		Errors:            []string{"boom"},
	})
	for _, want := range []string{"missing file", "drifted file", "missing merged key", "error boom"} {
		if !strings.Contains(rendered.String(), want) {
			t.Fatalf("printReconcileStatus missing %q:\n%s", want, rendered.String())
		}
	}
	if pluralInstall(2) != "installs" {
		t.Fatal("pluralInstall(2) should be installs")
	}
}

func TestInstallRunAndLoadResourceErrorBranches(t *testing.T) {
	d := setDotpackEnvForSyncTest(t)
	cmd := newInstallCmd()
	skill := filepath.Join(t.TempDir(), "SKILL.md")
	mustWriteTestFile(t, skill, "---\nname: s\ndescription: d\n---\nbody\n")
	if err := runInstall(cmd, skill, "claude-code", "skill", "bad-scope", false, false, false); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("runInstall bad scope err=%v; want scope", err)
	}
	if err := runInstall(cmd, skill, "missing-agent", "skill", "user", false, false, false); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("runInstall unknown agent err=%v; want unknown agent", err)
	}
	withPostInstallLifecycle(t, func(agent string) error { return errors.New("lifecycle boom") })
	if err := runInstall(cmd, skill, "claude-code", "skill", "user", false, true, true); err == nil || !strings.Contains(err.Error(), "post-install lifecycle failed") {
		t.Fatalf("runInstall lifecycle err=%v; want lifecycle failed", err)
	}

	if isDirectAgentsCommandPath(filepath.Join(d.ProjectHome, ".agents", "commands", "x.txt")) {
		t.Fatal("non-md/toml command path should be false")
	}
	if _, err := loadResource(resource.KindSkill, filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("loadResource missing err=%v; want read", err)
	}
	fixtures := []struct {
		kind resource.Kind
		name string
		raw  string
	}{
		{resource.KindAgent, "bad-agent.md", "---\nname: Bad Name\n---\n"},
		{resource.KindMCPServer, "bad-mcp.json", `{"mcpServers":{"bad name":{"command":"x"}}}`},
		{resource.KindHook, "Bad Hook.json", `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"x"}]}]}}`},
		{resource.KindRule, "bad-rule.md", "---\nname: Bad Name\n---\nbody\n"},
		{resource.KindCommand, "bad-command.md", "---\ndescription: d\nallowed-tools:\n  - 1\n---\nbody\n"},
	}
	for _, fx := range fixtures {
		path := filepath.Join(t.TempDir(), fx.name)
		mustWriteTestFile(t, path, fx.raw)
		if _, err := loadResource(fx.kind, path); err == nil {
			t.Fatalf("loadResource(%s) expected error", fx.kind)
		}
	}
}
