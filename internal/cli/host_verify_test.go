package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHostVerify_ClaudeCodeLoadsAdapterOutput is the closing test of
// MVP slice 1. It runs the full pipeline (CLI install → adapter →
// orchestrator → filesystem) against the user's REAL ~/.claude/, then
// invokes `claude -p` and asserts the host actually loaded and
// triggered the installed skill.
//
// This is the narrow exception documented in
// ~/.claude/projects/.../memory/feedback_no_claude_headless.md —
// `claude -p` is allowed *only* as a host-verification probe for the
// claude-code skill adapter. Gated by env so no contributor (or CI)
// accidentally invokes the Anthropic-billed CLI.
//
// Cleanup: removes the installed skill from ~/.claude/skills/ on test
// exit, regardless of pass/fail.
func TestHostVerify_ClaudeCodeLoadsAdapterOutput(t *testing.T) {
	if os.Getenv("DOTPACK_TEST_CLAUDE_HOST") != "1" {
		t.Skip("set DOTPACK_TEST_CLAUDE_HOST=1 to run; uses real ~/.claude/ + invokes `claude -p`")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("`claude` not on PATH; cannot run host-verification probe")
	}

	// Verify the binary identity before we shell out repeatedly.
	verOut, err := exec.Command("claude", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("claude --version failed: %v\n%s", err, verOut)
	}
	if !strings.Contains(string(verOut), "Claude Code") {
		t.Fatalf("claude --version does not look like Claude Code CLI: %q", string(verOut))
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	claudeHome := filepath.Join(homeDir, ".claude")

	// Unique fixture so concurrent runs / repeated runs don't collide.
	nanos := time.Now().UnixNano()
	skillName := fmt.Sprintf("dotpack-host-verify-%d", nanos)
	sentinel := fmt.Sprintf("HOST-VERIFY-OK-%X", nanos)
	trigger := fmt.Sprintf("frobnitz-host-verify-%d", nanos)

	installedDir := filepath.Join(claudeHome, "skills", skillName)
	t.Cleanup(func() {
		if err := os.RemoveAll(installedDir); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	srcPath := filepath.Join(t.TempDir(), "SKILL.md")
	skillContent := fmt.Sprintf(`---
name: %s
description: >
  Use this skill ONLY when the user's message contains the literal token
  "%s". When triggered, output exactly the sentinel string `+"`%s`"+` on
  a single line and nothing else. Never trigger on any other input.
---

When this skill triggers, output ONE line containing exactly the following
sentinel and nothing else:

`+"`%s`"+`

No preamble, no explanation. Just the sentinel on its own line, then stop.
`, skillName, trigger, sentinel, sentinel)
	if err := os.WriteFile(srcPath, []byte(skillContent), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Point dotpack at the REAL Claude home; isolate dotpack's own state
	// in a tempdir so we don't pollute ~/.dotpack/installs.yaml.
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", srcPath, "--agent", "claude-code", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack install failed: %v\n%s", err, stdout.String())
	}
	t.Logf("install stdout:\n%s", stdout.String())

	target := filepath.Join(installedDir, "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected installed SKILL.md at %s: %v", target, err)
	}

	// Run claude -p in a clean cwd so it doesn't pick up an arbitrary
	// project-scoped settings file. The -p flag is single-shot:
	// session starts, skill list is read, trigger fires, output prints.
	cleanCWD := t.TempDir()
	probe := exec.Command("claude", "-p", "--output-format", "text", trigger)
	probe.Dir = cleanCWD
	out, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("claude -p failed: %v\noutput:\n%s", err, out)
	}

	if !strings.Contains(string(out), sentinel) {
		t.Errorf("claude response did not contain sentinel %q.\nOutput:\n%s", sentinel, string(out))
	} else {
		t.Logf("host-verify OK: sentinel %q found in `claude -p` response", sentinel)
	}
}

// TestHostVerify_ClaudeCodeLoadsAdapterOutput_WithRuntimeOverrides is the
// slice-2 closing probe. Same pattern as the slice-1 test, but the source
// SKILL.md carries `allowed-tools` in its frontmatter — one of the
// claude_skill_runtime_overrides aliases (ADR-0016 §8). Slice 2's
// schema-driven §8 algorithm says claude-code natively supports this
// concept, so the adapter must (a) not flag the install lossy and
// (b) byte-pass-through the bytes (ADR-0008) so Claude Code's loader
// sees the field. This test fails if either guarantee regresses.
//
// Gated by DOTPACK_TEST_CLAUDE_HOST=1 (same as the slice-1 test) —
// `claude -p` is allowed only for skill-install verification per the
// narrow exception in memory/feedback_no_claude_headless.md. Do not
// widen the exception.
func TestHostVerify_ClaudeCodeLoadsAdapterOutput_WithRuntimeOverrides(t *testing.T) {
	if os.Getenv("DOTPACK_TEST_CLAUDE_HOST") != "1" {
		t.Skip("set DOTPACK_TEST_CLAUDE_HOST=1 to run; uses real ~/.claude/ + invokes `claude -p`")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("`claude` not on PATH; cannot run host-verification probe")
	}

	verOut, err := exec.Command("claude", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("claude --version failed: %v\n%s", err, verOut)
	}
	if !strings.Contains(string(verOut), "Claude Code") {
		t.Fatalf("claude --version does not look like Claude Code CLI: %q", string(verOut))
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	claudeHome := filepath.Join(homeDir, ".claude")

	nanos := time.Now().UnixNano()
	skillName := fmt.Sprintf("dotpack-host-verify-allowed-tools-%d", nanos)
	sentinel := fmt.Sprintf("HOST-VERIFY-ALLOWED-TOOLS-OK-%X", nanos)
	trigger := fmt.Sprintf("frobnitz-allowed-tools-%d", nanos)

	installedDir := filepath.Join(claudeHome, "skills", skillName)
	t.Cleanup(func() {
		if err := os.RemoveAll(installedDir); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	// The load-bearing difference from the slice-1 fixture: allowed-tools
	// is present in the frontmatter. The schema declares this is a
	// claude-code-native concept, so the install must succeed without
	// --allow-lossy and the field must survive byte-perfect on disk.
	srcPath := filepath.Join(t.TempDir(), "SKILL.md")
	skillContent := fmt.Sprintf(`---
name: %s
description: >
  Use this skill ONLY when the user's message contains the literal token
  "%s". When triggered, output exactly the sentinel string `+"`%s`"+` on
  a single line and nothing else. Never trigger on any other input.
allowed-tools:
  - Read
---

When this skill triggers, output ONE line containing exactly the following
sentinel and nothing else:

`+"`%s`"+`

No preamble, no explanation. Just the sentinel on its own line, then stop.
`, skillName, trigger, sentinel, sentinel)
	if err := os.WriteFile(srcPath, []byte(skillContent), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	// No --allow-lossy: the install must succeed natively because the
	// schema says claude-code supports allowed-tools. If it fails here,
	// the slice-2 §8 algorithm has regressed.
	cmd.SetArgs([]string{"install", srcPath, "--agent", "claude-code", "--scope", "user"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack install failed (slice-2 lossy regression?): %v\n%s", err, stdout.String())
	}
	t.Logf("install stdout:\n%s", stdout.String())

	target := filepath.Join(installedDir, "SKILL.md")
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read installed SKILL.md at %s: %v", target, err)
	}
	if !bytes.Equal(onDisk, []byte(skillContent)) {
		t.Fatalf("installed bytes diverge from source (ADR-0008 regression).\n"+
			"--- source ---\n%s\n--- on disk ---\n%s", skillContent, string(onDisk))
	}
	if !bytes.Contains(onDisk, []byte("allowed-tools:")) {
		t.Errorf("allowed-tools missing from installed SKILL.md — schema-keep regression")
	}

	cleanCWD := t.TempDir()
	probe := exec.Command("claude", "-p", "--output-format", "text", trigger)
	probe.Dir = cleanCWD
	out, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("claude -p failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), sentinel) {
		t.Errorf("claude response did not contain sentinel %q (loader may have rejected allowed-tools).\nOutput:\n%s", sentinel, string(out))
	} else {
		t.Logf("host-verify-with-allowed-tools OK: sentinel %q found", sentinel)
	}
}
