package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall_SkillEndToEnd(t *testing.T) {
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "user"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}

	got := stdout.String()
	if !strings.Contains(got, "Installed claude-code:skill:dotpack-tracer-bullet") {
		t.Errorf("expected success message; got %q", got)
	}

	target := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected SKILL.md at %s: %v", target, err)
	}

	manifestPath := filepath.Join(dotpackHome, "installs.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("expected manifest at %s: %v", manifestPath, err)
	}
}

func TestInstall_UnknownAgentErrors(t *testing.T) {
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "imaginary-host"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "imaginary-host") {
		t.Errorf("error should name the unknown agent; got %v", err)
	}
}

func TestInstall_MissingSourceErrors(t *testing.T) {
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", "/no/such/file/SKILL.md"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

func TestInstall_CollisionRefused_BypassedWithForce(t *testing.T) {
	// Slice 2 task #3 end-to-end via the CLI: install once → succeeds;
	// hand-edit the installed file → re-install with the manifest
	// removed simulates an untracked file at the target → CLI prints
	// CollisionError; --force re-installs and overwrites.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// First install: clean.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Wipe the manifest so the installed file is now untracked from
	// dotpack's perspective. Equivalent to a user who edited the file
	// and lost the manifest, or a file written by some other tool.
	if err := os.Remove(filepath.Join(dotpackHome, "installs.yaml")); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}

	// Second install with no --force: refuses with CollisionError.
	cmd2 := NewRootCmd()
	cmd2.SetOut(io_DiscardWriter())
	cmd2.SetErr(io_DiscardWriter())
	cmd2.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "user"})
	err := cmd2.Execute()
	if err == nil {
		t.Fatal("expected collision refusal, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "collide") || !strings.Contains(msg, "--force") {
		t.Errorf("CLI error should describe collision + suggest --force; got %q", msg)
	}

	// Third install with --force: succeeds.
	cmd3 := NewRootCmd()
	cmd3.SetOut(io_DiscardWriter())
	cmd3.SetErr(io_DiscardWriter())
	cmd3.SetArgs([]string{"install", src, "--agent", "claude-code", "--scope", "user", "--force"})
	if err := cmd3.Execute(); err != nil {
		t.Fatalf("install --force: %v", err)
	}
}

func TestInstall_InfersSkillKindFromFilename(t *testing.T) {
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src}) // no --kind

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install without --kind should infer skill from SKILL.md filename; got %v", err)
	}
}

// io_DiscardWriter is a tiny helper to silence cobra output in tests
// that only check the error. Avoids pulling in io.Discard imports.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func io_DiscardWriter() discardWriter              { return discardWriter{} }
