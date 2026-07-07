package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstall_AgentEndToEnd(t *testing.T) {
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)

	src := filepath.Join("..", "resource", "testdata", "agents", "dotpack-tracer-agent.md")

	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "agent", "--scope", "user"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install: %v\n%s", err, stdout.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "Installed claude-code:agent:dotpack-tracer-agent") {
		t.Errorf("expected agent install success; got %q", got)
	}
	target := filepath.Join(claudeHome, "agents", "dotpack-tracer-agent.md")
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected agent file at %s: %v", target, err)
	}
	// Content-level assertions — without these, a regression where
	// planAgent writes empty bytes / drops description / emits tools
	// in YAML-array form would still pass the file-existence check.
	body := string(onDisk)
	if !strings.Contains(body, "name: dotpack-tracer-agent") {
		t.Errorf("installed agent file missing name field; got:\n%s", body)
	}
	if !strings.Contains(body, "ECHO-AGENT-TRACER-7E4F1D8C") {
		t.Errorf("installed agent file missing body sentinel; got:\n%s", body)
	}
	// Tools must be in claude's preferred comma-separated string form,
	// NOT a YAML array. Pin both directions so a future regression where
	// planAgent pass-throughs YAML-array source is caught.
	if !strings.Contains(body, "tools: Read, Write, Edit") {
		t.Errorf("installed agent file must emit tools as comma-string; got:\n%s", body)
	}
	if strings.Contains(body, "tools:\n  - Read") || strings.Contains(body, "tools:\n- Read") {
		t.Errorf("installed agent file must NOT emit tools as YAML array; got:\n%s", body)
	}
}

func TestInstall_AgentKindNotInferred_RequiresExplicitFlag(t *testing.T) {
	// Skills infer from SKILL.md filename; agents have no canonical
	// filename (<agent-name>.md collides with anything else). Inference
	// for agent would mean "any .md is an agent", which is dangerously
	// permissive — we'd treat a misplaced SKILL.md (mis-named) as an
	// agent. So explicit --kind agent is required.
	t.Setenv("DOTPACK_CLAUDE_HOME", t.TempDir())
	t.Setenv("DOTPACK_DOTPACK_HOME", t.TempDir())
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())
	src := filepath.Join("..", "resource", "testdata", "agents", "dotpack-tracer-agent.md")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when --kind is omitted for an agent source, got nil")
	}
	if !strings.Contains(err.Error(), "infer --kind") {
		t.Errorf("error should explain that --kind cannot be inferred; got %v", err)
	}
}

func TestList_MixedSkillAndAgent_ShowsBoth(t *testing.T) {
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	skillSrc := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	agentSrc := filepath.Join("..", "resource", "testdata", "agents", "dotpack-tracer-agent.md")

	for _, args := range [][]string{
		{"install", skillSrc, "--agent", "claude-code"},
		{"install", agentSrc, "--agent", "claude-code", "--kind", "agent"},
	} {
		cmd := NewRootCmd()
		cmd.SetOut(io_DiscardWriter())
		cmd.SetErr(io_DiscardWriter())
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("setup install %v: %v", args, err)
		}
	}

	var out bytes.Buffer
	list := NewRootCmd()
	list.SetOut(&out)
	list.SetErr(&out)
	list.SetArgs([]string{"list"})
	if err := list.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "claude-code:skill:dotpack-tracer-bullet") {
		t.Errorf("list missing skill row; got %q", got)
	}
	if !strings.Contains(got, "claude-code:agent:dotpack-tracer-agent") {
		t.Errorf("list missing agent row; got %q", got)
	}
}

func TestUninstall_Agent_ByID_RemovesFileLeavesSharedDir(t *testing.T) {
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "agents", "dotpack-tracer-agent.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "agent"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	var out bytes.Buffer
	uninstall := NewRootCmd()
	uninstall.SetOut(&out)
	uninstall.SetErr(&out)
	uninstall.SetArgs([]string{"uninstall", "claude-code:agent:dotpack-tracer-agent"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}

	if _, err := os.Stat(filepath.Join(claudeHome, "agents", "dotpack-tracer-agent.md")); !os.IsNotExist(err) {
		t.Errorf("agent file should be removed; got %v", err)
	}
	if _, err := os.Stat(filepath.Join(claudeHome, "agents")); err != nil {
		t.Errorf("agents/ dir must survive; got %v", err)
	}
	got := out.String()
	if strings.Contains(got, "removed directory") {
		t.Errorf("output must not claim it removed the shared agents/ dir; got %q", got)
	}
	// Agent's record TargetDir is "" — the CLI's "if rec.TargetDir != ''"
	// branch should not fire at all; no "kept directory" line either.
	if strings.Contains(got, "kept directory") {
		t.Errorf("output must not mention a kept directory for agents (TargetDir is empty); got %q", got)
	}
}

func TestUninstall_Agent_ShortName_WithExplicitKindFlag(t *testing.T) {
	// Short-name uninstall path for agents. Without --kind agent, the
	// CLI's default --kind=skill would compose claude-code:skill:<name>
	// and fail the manifest lookup (covered by the "did you mean" test
	// below). With --kind agent, it must resolve correctly.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "agents", "dotpack-tracer-agent.md")

	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "agent"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "dotpack-tracer-agent", "--agent", "claude-code", "--kind", "agent"})
	if err := uninstall.Execute(); err != nil {
		t.Fatalf("uninstall short-name with --kind agent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claudeHome, "agents", "dotpack-tracer-agent.md")); !os.IsNotExist(err) {
		t.Errorf("agent file should be removed; got %v", err)
	}
}

func TestUninstall_Agent_WrongKindDefault_ErrorHintsActualID(t *testing.T) {
	// Footgun: user installs agent, types `dotpack uninstall my-agent
	// --agent claude-code` (forgetting --kind agent). Default --kind=skill
	// composes claude-code:skill:my-agent → manifest miss. The error must
	// surface the actual matching ID(s) so the user can fix it in one
	// shot, not silently misroute.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	src := filepath.Join("..", "resource", "testdata", "agents", "dotpack-tracer-agent.md")
	install := NewRootCmd()
	install.SetOut(io_DiscardWriter())
	install.SetErr(io_DiscardWriter())
	install.SetArgs([]string{"install", src, "--agent", "claude-code", "--kind", "agent"})
	if err := install.Execute(); err != nil {
		t.Fatalf("install: %v", err)
	}

	uninstall := NewRootCmd()
	uninstall.SetOut(io_DiscardWriter())
	uninstall.SetErr(io_DiscardWriter())
	uninstall.SetArgs([]string{"uninstall", "dotpack-tracer-agent", "--agent", "claude-code"})
	err := uninstall.Execute()
	if err == nil {
		t.Fatal("expected error for misrouted kind, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean") {
		t.Errorf("error must hint at the actual matching ID; got %q", msg)
	}
	if !strings.Contains(msg, "claude-code:agent:dotpack-tracer-agent") {
		t.Errorf("error must name the agent's actual ID; got %q", msg)
	}
}

func TestUninstall_MixedKindSameShortName_AreIndependent(t *testing.T) {
	// Distinct full IDs (claude-code:skill:foo vs claude-code:agent:foo)
	// must be addressable independently via short-name + --kind. Pin that
	// uninstalling one leaves the other intact — guards against any
	// future change to resolveUninstallID accidentally targeting both.
	claudeHome := t.TempDir()
	dotpackHome := t.TempDir()
	t.Setenv("DOTPACK_CLAUDE_HOME", claudeHome)
	t.Setenv("DOTPACK_DOTPACK_HOME", dotpackHome)
	t.Setenv("DOTPACK_PROJECT_HOME", t.TempDir())

	// Write a skill and an agent with the same short name "twin".
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "twin-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: twin\ndescription: skill twin\n---\nskill body\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	agentPath := filepath.Join(tmp, "twin.md")
	if err := os.WriteFile(agentPath, []byte("---\nname: twin\ndescription: agent twin\n---\nagent body\n"), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	for _, args := range [][]string{
		{"install", skillPath, "--agent", "claude-code"},
		{"install", agentPath, "--agent", "claude-code", "--kind", "agent"},
	} {
		cmd := NewRootCmd()
		cmd.SetOut(io_DiscardWriter())
		cmd.SetErr(io_DiscardWriter())
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("install %v: %v", args, err)
		}
	}

	// Uninstall just the agent by short-name + --kind agent.
	un := NewRootCmd()
	un.SetOut(io_DiscardWriter())
	un.SetErr(io_DiscardWriter())
	un.SetArgs([]string{"uninstall", "twin", "--agent", "claude-code", "--kind", "agent"})
	if err := un.Execute(); err != nil {
		t.Fatalf("uninstall agent: %v", err)
	}

	if _, err := os.Stat(filepath.Join(claudeHome, "agents", "twin.md")); !os.IsNotExist(err) {
		t.Errorf("agent twin should be removed; got %v", err)
	}
	if _, err := os.Stat(filepath.Join(claudeHome, "skills", "twin", "SKILL.md")); err != nil {
		t.Errorf("skill twin must survive; got %v", err)
	}
}
