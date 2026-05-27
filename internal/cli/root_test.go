package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootWithNoArgsShowsUsage(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack with no args returned error: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "Usage:") {
		t.Errorf("dotpack usage output missing 'Usage:' header: %q", got)
	}
	if !strings.Contains(got, "version") {
		t.Errorf("dotpack usage missing 'version' subcommand: %q", got)
	}
	if !strings.Contains(got, "agent") {
		t.Errorf("dotpack usage missing domain tagline (expected 'agent'): %q", got)
	}
}

func TestInstallHelpDocumentsAgentsToHostTranslation(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack install --help returned error: %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		".agents -> host-native",
		".claude/skills/<name>/SKILL.md",
		".gemini/skills/<name>/SKILL.md",
		".codex/config.toml",
		"agents-cli  -> .agents/skills/<name>/SKILL.md once for Gemini + Codex",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("install help missing %q:\n%s", want, got)
		}
	}
}

func TestImportHelpPointsToInstallForAgentsToHostTranslation(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"import", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack import --help returned error: %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"To translate .agents resources back into .claude, .gemini, .codex/config.toml",
		"use dotpack install on the specific",
		"does not yet bulk-export an entire .agents tree",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("import help missing %q:\n%s", want, got)
		}
	}
}
