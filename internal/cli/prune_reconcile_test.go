package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReconcile_ReportsMissingFileWithoutMutatingManifest(t *testing.T) {
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "user"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	target := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove installed file: %v", err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"reconcile"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("reconcile: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if !strings.Contains(got, "claude-code:skill:dotpack-tracer-bullet") {
		t.Fatalf("reconcile output should name drifting install; got %q", got)
	}
	if !strings.Contains(got, "missing file") || !strings.Contains(got, target) {
		t.Errorf("reconcile output should report missing file %s; got %q", target, got)
	}

	records := readManifestRecords(t, dotpackHome)
	if len(records) != 1 {
		t.Fatalf("reconcile must not mutate manifest; got %d records", len(records))
	}
}

func TestPrune_RemovesFullyStaleFiledropRecordAndKeepsLiveRecord(t *testing.T) {
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "user"})
	if err := install.Execute(); err != nil {
		t.Fatalf("first install: %v", err)
	}

	target := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove installed file: %v", err)
	}
	targetDir := filepath.Dir(target)

	// Re-install under a different host leaves one live record beside the
	// stale claude-code one. Prune must remove only the fully stale row.
	geminiHome := t.TempDir()
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	second := NewRootCmd()
	second.SetOut(io_DiscardWriter())
	second.SetErr(io_DiscardWriter())
	second.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--scope", "user"})
	if err := second.Execute(); err != nil {
		t.Fatalf("second install: %v", err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"prune"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if !strings.Contains(got, "Pruned claude-code:skill:dotpack-tracer-bullet") {
		t.Errorf("prune output should name stale record; got %q", got)
	}

	records := readManifestRecords(t, dotpackHome)
	if len(records) != 1 {
		t.Fatalf("expected one live record after prune; got %d: %+v", len(records), records)
	}
	if records[0].ID != "gemini-cli:skill:dotpack-tracer-bullet" {
		t.Fatalf("prune removed wrong record; remaining records: %+v", records)
	}
	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Fatalf("expected empty stale target dir removed, stat err = %v", err)
	}
}

func TestPrune_RemovesFullyStaleMergedKeyRecord(t *testing.T) {
	geminiHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_GEMINI_HOME", geminiHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "mcp-servers", "github.mcp.json")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "gemini-cli", "--kind", "mcp-server", "--scope", "user"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	settingsPath := filepath.Join(geminiHome, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("clear merged key from settings: %v", err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"prune"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune: %v\n%s", err, stdout.String())
	}

	if got := stdout.String(); !strings.Contains(got, "Pruned gemini-cli:mcp-server:github") {
		t.Errorf("prune output should name stale merged-key record; got %q", got)
	}
	records := readManifestRecords(t, dotpackHome)
	if len(records) != 0 {
		t.Fatalf("expected stale merged-key record pruned; got %+v", records)
	}
}

func TestPrune_KeepsPartialRecord(t *testing.T) {
	codexHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CODEX_HOME", codexHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join("..", "resource", "testdata", "hooks", "multi-binding.hook.json")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "codex", "--kind", "hook", "--scope", "user"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	configPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(configPath, []byte("[hooks]\n[[hooks.PreToolUse]]\nmatcher = 'Bash'\n\n[[hooks.PreToolUse.hooks]]\ncommand = '/usr/local/bin/pre-bash.sh'\ntype = 'command'\n"), 0o644); err != nil {
		t.Fatalf("partially clear config: %v", err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"prune"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("prune: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if strings.Contains(got, "Pruned codex:hook:multi-binding") {
		t.Fatalf("prune must keep partially-present records; got %q", got)
	}
	if !strings.Contains(got, "kept 1 partially present install") {
		t.Errorf("prune should report partial records were kept; got %q", got)
	}

	records := readManifestRecords(t, dotpackHome)
	if len(records) != 1 || records[0].ID != "codex:hook:multi-binding" {
		t.Fatalf("partial record should remain; got %+v", records)
	}
}

type manifestRecordForTest struct {
	ID string `yaml:"id"`
}

func readManifestRecords(t *testing.T, dotpackHome string) []manifestRecordForTest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dotpackHome, "installs.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Installs []manifestRecordForTest `yaml:"installs"`
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v\n%s", err, raw)
	}
	return m.Installs
}
