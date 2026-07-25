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
	if !strings.Contains(got, "scan-skills") {
		t.Errorf("dotpack usage missing 'scan-skills' subcommand: %q", got)
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
		"agents-cli      -> .agents/skills/<name>/SKILL.md once for sub-adapters",
		"Pass --run-lifecycle to run",
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
		"use dotpack install on a specific",
		"dotpack install-all for a full canonical .agents tree",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("import help missing %q:\n%s", want, got)
		}
	}
}

func TestScanSkillsHelpDocumentsSourceShapesAndGateMode(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"scan-skills", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dotpack scan-skills --help returned error: %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"NVIDIA SkillSpector",
		"a skill directory containing SKILL.md",
		"a canonical .agents tree",
		"returns non-zero when unsuppressed findings remain",
		"--report-only",
		"--skill-bypass-security",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("scan-skills help missing %q:\n%s", want, got)
		}
	}
}
