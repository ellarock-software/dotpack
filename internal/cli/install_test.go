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

func TestUninstall_EndToEnd_RemovesFileAndManifestRecord(t *testing.T) {
	// Slice 3 #5 happy path via the CLI: install → uninstall by full
	// ID → SKILL.md gone, install record gone. Mirrors install's
	// end-to-end test in shape.
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
	installedFile := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(installedFile); err != nil {
		t.Fatalf("pre-condition: installed file missing: %v", err)
	}

	var stdout bytes.Buffer
	uninstall := NewRootCmd()
	uninstall.SetOut(&stdout)
	uninstall.SetErr(&stdout)
	uninstall.SetArgs([]string{"uninstall", "claude-code:skill:dotpack-tracer-bullet"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, stdout.String())
	}

	if _, err := os.Stat(installedFile); !os.IsNotExist(err) {
		t.Errorf("file should be gone after uninstall; stat: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "Uninstalled claude-code:skill:dotpack-tracer-bullet") {
		t.Errorf("expected success message naming the install; got %q", got)
	}
	if !strings.Contains(got, installedFile) {
		t.Errorf("uninstall output should list the removed path %q; got %q", installedFile, got)
	}
}

func TestUninstall_ByName_DefaultsAgentAndKind(t *testing.T) {
	// Ergonomic shortcut: `dotpack uninstall <name>` composes
	// `<agent>:<kind>:<name>` from the --agent / --kind defaults so the
	// user doesn't have to type the full ID. Mirrors `dotpack install
	// SKILL.md` defaulting --agent=claude-code and inferring --kind=skill.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "dotpack-tracer-bullet", "--kind", "skill"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall by short name: %v", err)
	}

	target := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone after short-name uninstall; stat: %v", err)
	}
}

func TestUninstall_FullIDBypassesAgentFlag(t *testing.T) {
	// Hostile-review #1: resolveUninstallID's docstring promises that a
	// handle containing ":" is treated as a full ID verbatim — the user
	// is being explicit, so a typo'd or skewed --agent flag must not
	// silently reshape the lookup. But runUninstall used to call
	// buildAdapter(agentName) unconditionally, which errored on any
	// --agent value that isn't claude-code regardless of what's in the
	// ID. Once a second adapter lands (gemini-cli per slice 3 task #7)
	// this would break copy-paste-the-ID workflows on day one. The fix:
	// when the handle is a full ID, the host segment IS the source of
	// truth — --agent is ignored. Below we pass `--agent gemini-cli`
	// (the actual second adapter, divergent from the ID's host segment
	// `claude-code`) to exercise the case the comment describes.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	// --agent set to a value the install command would refuse, but the
	// handle is a full ID — uninstall MUST honour the ID and ignore
	// --agent. Otherwise pasting an ID with a non-default host fails.
	uninstall.SetArgs([]string{"uninstall", "claude-code:skill:dotpack-tracer-bullet", "--agent", "gemini-cli"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall by full ID with mismatched --agent should succeed; got %v", err)
	}

	target := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be gone after full-ID uninstall; stat: %v", err)
	}
}

func TestUninstall_OutputDistinguishesRemovedFromMissing(t *testing.T) {
	// Hostile-review #3: the CLI used to print "removed X" for every
	// path in rec.Files, even when X was already missing — silently
	// lying about what dotpack actually did. The honest output:
	//   - "removed <path>" for files dotpack deleted
	//   - "skipped <path> (already gone)" for files that were missing
	//   - "removed directory <targetDir>" / "kept directory <td> (not empty)"
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}
	installedFile := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet", "SKILL.md")
	// Delete the installed file behind dotpack's back to exercise the
	// "missing" branch.
	if err := os.Remove(installedFile); err != nil {
		t.Fatalf("setup remove file: %v", err)
	}

	var stdout bytes.Buffer
	uninstall := NewRootCmd()
	uninstall.SetOut(&stdout)
	uninstall.SetErr(&stdout)
	uninstall.SetArgs([]string{"uninstall", "dotpack-tracer-bullet"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got := stdout.String()
	if strings.Contains(got, "removed "+installedFile) {
		t.Errorf("output should NOT claim 'removed' for a file that was already gone; got %q", got)
	}
	if !strings.Contains(got, "skipped "+installedFile+" (already gone)") {
		t.Errorf("output should show 'skipped (already gone)' for the missing file; got %q", got)
	}
}

func TestUninstall_OutputReportsKeptDirectoryWhenNotEmpty(t *testing.T) {
	// Companion to TargetDirRemoved: when a sibling file kept the dir
	// non-empty, the CLI must say so rather than silently leaving the
	// dir on disk with no indication.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}
	targetDir := filepath.Join(claudeHome, "skills", "dotpack-tracer-bullet")
	if err := os.WriteFile(filepath.Join(targetDir, "NOTES.md"), []byte("user"), 0o644); err != nil {
		t.Fatalf("setup stray: %v", err)
	}

	var stdout bytes.Buffer
	uninstall := NewRootCmd()
	uninstall.SetOut(&stdout)
	uninstall.SetErr(&stdout)
	uninstall.SetArgs([]string{"uninstall", "dotpack-tracer-bullet"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, "kept directory "+targetDir+" (not empty)") {
		t.Errorf("expected 'kept directory ... (not empty)'; got %q", got)
	}
}

func TestUninstall_UnknownID_Errors(t *testing.T) {
	// Loud error when the user asks to uninstall something that isn't
	// in the manifest — exit non-zero with a message naming the
	// missing ID. Silent "ok, did nothing" would mask typos.
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"uninstall", "claude-code:skill:nope"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown ID, got nil")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the missing ID; got %v", err)
	}
}

func TestList_NoInstalls_RendersEmpty(t *testing.T) {
	// Fresh system: manifest doesn't exist yet. `dotpack list` prints
	// a friendly empty marker rather than erroring.
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list on empty: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(strings.ToLower(got), "no installs") {
		t.Errorf("expected 'no installs' marker; got %q", got)
	}
}

func TestList_AfterInstall_ShowsRecord(t *testing.T) {
	// One install → one line in list. Output includes the full ID (the
	// unambiguous uninstall handle, per advisor #5) and the scope.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	var stdout bytes.Buffer
	list := NewRootCmd()
	list.SetOut(&stdout)
	list.SetErr(&stdout)
	list.SetArgs([]string{"list"})
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := stdout.String()
	for _, want := range []string{"claude-code:skill:dotpack-tracer-bullet", "user"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q; full output:\n%s", want, got)
		}
	}
}

// io_DiscardWriter is a tiny helper to silence cobra output in tests
// that only check the error. Avoids pulling in io.Discard imports.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func io_DiscardWriter() discardWriter             { return discardWriter{} }
