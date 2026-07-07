package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/manifest"
)

// Default-agent misroute hint (slice-3 #7-followon). Symmetric to the
// uninstall-side cross-host hint that slice 3 #6 added in
// orchestrator.Uninstall. The install side has TWO asymmetries vs
// uninstall worth pinning in tests:
//
//  1. Trigger is defaulted-vs-explicit on --agent, not lookup-miss.
//     The hint must NOT fire when the user types --agent claude-code
//     explicitly (an active opt-in to the default value).
//  2. Match is on the (kind, name) TUPLE, not just the trailing
//     short-name. Install always has a concrete resource.Kind() from
//     resolveKind/loadResource, so a same-name-different-kind record on
//     another host is a distinct concept and must NOT trigger the hint.
//     Uninstall can't be this sharp because default --kind=skill is
//     itself a misroute candidate.

// envPaths bundles the tempdir paths setupTriHostEnv installs as
// DOTPACK_* env vars. Returning a struct (rather than positional
// tuples) keeps test reads self-documenting and — critically — avoids
// the side-channel `os.Getenv("DOTPACK_CLAUDE_HOME")` pattern that
// slice-3 #8 hostile-review #3 caught. A test that needs claudeHome
// reads `env.claude`; the dependency is structural and visible, not
// laundered through a global the helper happens to set.
type envPaths struct {
	claude  string
	gemini  string
	agents  string
	dotpack string
	project string
}

// setupTriHostEnv installs fresh tempdir-backed DOTPACK_* env vars for
// every supported host root in one call, and returns each path in a
// struct so tests can reference them without re-reading the process
// environment. Use this for tests that exercise install/uninstall paths
// across multiple --agent values (the misroute-hint tests are the
// motivating case). For single-host tests prefer the per-host helpers
// (setupGeminiEnv / setupCodexEnv) so the test's host scope stays
// legible.
func setupTriHostEnv(t *testing.T) envPaths {
	t.Helper()
	p := envPaths{
		claude:  t.TempDir(),
		gemini:  t.TempDir(),
		agents:  t.TempDir(),
		dotpack: t.TempDir(),
		project: t.TempDir(),
	}
	t.Setenv("DOTPACK_CLAUDE_HOME", p.claude)
	t.Setenv("DOTPACK_GEMINI_HOME", p.gemini)
	t.Setenv("DOTPACK_AGENTS_HOME", p.agents)
	t.Setenv("DOTPACK_DOTPACK_HOME", p.dotpack)
	t.Setenv("DOTPACK_PROJECT_HOME", p.project)
	return p
}

// TestInstall_DefaultAgent_AlternateHostHasSameKindName_HintsAndRefuses
// pins the primary contract: when --agent was defaulted (i.e. left at
// claude-code) and the manifest already has a (skill, dotpack-tracer-bullet)
// record on gemini-cli, the install must refuse with a "did you mean
// --agent gemini-cli?" message AND must NOT write the SKILL.md to the
// claude-code skills directory. The no-file-written assertion is the
// anti-theatre guard — a hint that fires AFTER the write would pass a
// message-only assertion but leave a real misroute on disk.
func TestInstall_DefaultAgent_AlternateHostHasSameKindName_HintsAndRefuses(t *testing.T) {
	env := setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// Seed: install on gemini-cli explicitly so manifest has
	// gemini-cli:skill:dotpack-tracer-bullet but not claude-code:...
	seed := NewRootCmd()
	seed.SetOut(io_DiscardWriter())
	seed.SetErr(io_DiscardWriter())
	seed.SetArgs([]string{"install", src, "--agent", "gemini-cli"})
	if err := seed.Execute(); err != nil {
		t.Fatalf("seed install on gemini-cli: %v", err)
	}

	// Trigger: install with no --agent → defaults to claude-code.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected misroute hint error when --agent defaulted, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean") {
		t.Errorf("error must include 'did you mean'; got %q", msg)
	}
	if !strings.Contains(msg, "--agent gemini-cli") {
		t.Errorf("error must name the alternate adapter as a fix; got %q", msg)
	}

	// Anti-theatre: the claude-code skills directory must NOT contain the
	// SKILL.md (the hint must short-circuit BEFORE the orchestrator writes
	// anything). A regression that moves the hint check after orch.Install
	// would pass the message asserts above but fail this one.
	wrongTarget := filepath.Join(env.claude, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, statErr := os.Stat(wrongTarget); !os.IsNotExist(statErr) {
		t.Errorf("misrouted install must not have written to claude-code path %s; got stat err %v",
			wrongTarget, statErr)
	}
}

// TestInstall_ExplicitAgentClaudeCode_SuppressesHint pins the escape
// hatch: passing --agent claude-code explicitly is an opt-in to the
// default value, so the hint MUST NOT fire even when the manifest has a
// matching (kind, name) on a different host. The corresponding positive
// assertion (the file IS written to the claude-code path) catches a
// regression that "suppression" accidentally skips the install too.
func TestInstall_ExplicitAgentClaudeCode_SuppressesHint(t *testing.T) {
	env := setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// Seed: install on gemini-cli (so the hint would otherwise fire).
	seed := NewRootCmd()
	seed.SetOut(io_DiscardWriter())
	seed.SetErr(io_DiscardWriter())
	seed.SetArgs([]string{"install", src, "--agent", "gemini-cli"})
	if err := seed.Execute(); err != nil {
		t.Fatalf("seed install on gemini-cli: %v", err)
	}

	// Trigger: explicit --agent claude-code → no hint, install proceeds.
	var stdout bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", src, "--agent", "claude-code"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("explicit --agent claude-code must suppress hint; got %v\n%s", err, stdout.String())
	}

	target := filepath.Join(env.claude, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("explicit claude-code install must write SKILL.md to %s; got %v", target, err)
	}
}

// TestInstall_EmptyManifest_DefaultAgent_NoHint is the success-path
// regression guard: a fresh system (no manifest entries) installing with
// the default --agent must succeed silently. Catches a "hint fires
// unconditionally" regression that would break first-time use entirely.
func TestInstall_EmptyManifest_DefaultAgent_NoHint(t *testing.T) {
	env := setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("empty-manifest defaulted install must succeed; got %v", err)
	}
	target := filepath.Join(env.claude, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("install must write to claude-code path %s; got %v", target, err)
	}
}

// TestInstall_DefaultAgent_OtherHostDifferentKind_NoHint pins the
// (kind, name) tuple filter: an agent named foo on gemini-cli is a
// DIFFERENT concept from a skill named foo. Installing the skill with
// the default agent must NOT hint even though the bare short-name
// collides — the kinds don't match. This is where install can be
// sharper than uninstall (uninstall's hint compares bare short-name
// because default --kind=skill is itself a misroute candidate; install
// always knows the kind from the source file).
func TestInstall_DefaultAgent_OtherHostDifferentKind_NoHint(t *testing.T) {
	env := setupTriHostEnv(t)

	// Build a skill and an agent that share the SAME name "twin" so the
	// only thing telling them apart is .Kind(). Inline files so the
	// shared-name collision is unambiguous (testdata fixtures use
	// different names per kind).
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "twin-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: twin\ndescription: skill twin\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	agentPath := filepath.Join(tmp, "twin-agent.md")
	if err := os.WriteFile(agentPath, []byte("---\nname: twin\ndescription: agent twin\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}

	// Seed: install the AGENT 'twin' on gemini-cli.
	seed := NewRootCmd()
	seed.SetOut(io_DiscardWriter())
	seed.SetErr(io_DiscardWriter())
	seed.SetArgs([]string{"install", agentPath, "--agent", "gemini-cli", "--kind", "agent"})
	if err := seed.Execute(); err != nil {
		t.Fatalf("seed agent on gemini-cli: %v", err)
	}

	// Trigger: install the SKILL 'twin' with defaulted --agent. Different
	// kind → no hint, install proceeds.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", skillPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("defaulted skill install with only an agent-kind name match must succeed; got %v", err)
	}
	target := filepath.Join(env.claude, "skills", "twin", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("skill must land at claude-code path %s; got %v", target, err)
	}
}

// TestInstall_DefaultAgent_TargetHostAlsoHasMatch_NoHint pins the
// legitimate-re-install path: when the manifest has the (kind, name)
// match BOTH on the defaulted target (claude-code) AND another host, the
// user has already demonstrated intent for the target — re-installing
// there is an update, not a misroute. The hint must NOT fire.
func TestInstall_DefaultAgent_TargetHostAlsoHasMatch_NoHint(t *testing.T) {
	env := setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// Seed both hosts: claude-code AND gemini-cli.
	for _, host := range []string{"claude-code", "gemini-cli"} {
		c := NewRootCmd()
		c.SetOut(io_DiscardWriter())
		c.SetErr(io_DiscardWriter())
		c.SetArgs([]string{"install", src, "--agent", host})
		if err := c.Execute(); err != nil {
			t.Fatalf("seed on %s: %v", host, err)
		}
	}

	// Trigger: defaulted --agent. Target already has the record → no hint.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("defaulted re-install on a host that already owns this (kind, name) must succeed; got %v", err)
	}
	target := filepath.Join(env.claude, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected re-installed SKILL.md at %s; got %v", target, err)
	}
}

// TestInstall_DefaultAgent_MultipleAlternateHosts_HintsAll pins
// completeness: when the (kind, name) tuple matches on multiple
// non-target hosts (gemini-cli AND codex), the hint must name all of
// them — the user can decide which one was intended. Order-independent
// substring assertion so a stable-sort regression doesn't flip the test
// brittly.
func TestInstall_DefaultAgent_MultipleAlternateHosts_HintsAll(t *testing.T) {
	setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// Seed both alternate hosts.
	for _, host := range []string{"gemini-cli", "codex"} {
		c := NewRootCmd()
		c.SetOut(io_DiscardWriter())
		c.SetErr(io_DiscardWriter())
		c.SetArgs([]string{"install", src, "--agent", host})
		if err := c.Execute(); err != nil {
			t.Fatalf("seed on %s: %v", host, err)
		}
	}

	// Trigger: defaulted --agent.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected misroute hint with two alternate hosts in manifest, got nil")
	}
	msg := err.Error()
	for _, host := range []string{"gemini-cli", "codex"} {
		if !strings.Contains(msg, host) {
			t.Errorf("error must mention alternate host %q; got %q", host, msg)
		}
	}
}

// TestInstall_DefaultAgent_StaleManifestHost_OnlyMatch_NoHint pins
// finding #2 from hostile-review: if the ONLY (kind, name) match in
// the manifest names a host the current binary cannot build (a removed
// or never-implemented adapter), the hint must NOT suggest that host —
// surfacing `did you mean --agent removed-adapter?` would move the user
// from one error to another with no progress. The defensive behavior
// is: drop unbuildable hosts during the alternate-set walk, and if no
// buildable alternates remain, treat the manifest as if it had no
// matching records (install proceeds normally on the default).
func TestInstall_DefaultAgent_StaleManifestHost_OnlyMatch_NoHint(t *testing.T) {
	env := setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// Inject a manifest entry for an unbuildable host directly via the
	// manifest store. Simulates the realistic case where dotpack
	// upgraded across an adapter rename or removal and the on-disk
	// manifest still references the old host string. We deliberately
	// bypass the install CLI for this seed because no valid `dotpack
	// install --agent removed-adapter` invocation exists.
	store := manifest.NewStore(filepath.Join(env.dotpack, "installs.yaml"))
	stale := manifest.Record{
		ID:          "removed-adapter:skill:dotpack-tracer-bullet",
		Source:      "file:///dev/null/SKILL.md",
		Kind:        "skill",
		Agent:       "removed-adapter",
		Scope:       "user",
		Files:       []string{"/dev/null/SKILL.md"},
		InstalledAt: "2026-01-01T00:00:00Z",
	}
	if err := store.Upsert(stale); err != nil {
		t.Fatalf("seed stale manifest entry: %v", err)
	}

	// Trigger: defaulted --agent. Only-match is unbuildable → no hint.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("defaulted install with only-stale-host match must succeed; got %v", err)
	}
	target := filepath.Join(env.claude, "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("install must write to claude-code path %s; got %v", target, err)
	}
}

// TestInstall_DefaultAgent_MixedStaleAndBuildableHosts_HintsOnlyBuildable
// pins the mixed case: when a (kind, name) match exists on a stale host
// AND a buildable host, the hint must fire (because at least one real
// alternate exists) but must NOT surface the stale host string in its
// message. Otherwise the user gets a hint listing two hosts, copies
// the stale one, and lands at "unknown agent".
func TestInstall_DefaultAgent_MixedStaleAndBuildableHosts_HintsOnlyBuildable(t *testing.T) {
	env := setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// Seed a real install on gemini-cli through the CLI (so the manifest
	// gets a legitimate gemini-cli:skill:dotpack-tracer-bullet record).
	seed := NewRootCmd()
	seed.SetOut(io_DiscardWriter())
	seed.SetErr(io_DiscardWriter())
	seed.SetArgs([]string{"install", src, "--agent", "gemini-cli"})
	if err := seed.Execute(); err != nil {
		t.Fatalf("seed on gemini-cli: %v", err)
	}

	// Inject an additional stale record alongside the real one.
	store := manifest.NewStore(filepath.Join(env.dotpack, "installs.yaml"))
	stale := manifest.Record{
		ID:          "removed-adapter:skill:dotpack-tracer-bullet",
		Source:      "file:///dev/null/SKILL.md",
		Kind:        "skill",
		Agent:       "removed-adapter",
		Scope:       "user",
		Files:       []string{"/dev/null/SKILL.md"},
		InstalledAt: "2026-01-01T00:00:00Z",
	}
	if err := store.Upsert(stale); err != nil {
		t.Fatalf("inject stale entry: %v", err)
	}

	// Trigger: defaulted --agent. Hint fires (gemini-cli is buildable)
	// but must NOT mention removed-adapter.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected hint with gemini-cli alternate, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--agent gemini-cli") {
		t.Errorf("hint must name --agent gemini-cli; got %q", msg)
	}
	if strings.Contains(msg, "removed-adapter") {
		t.Errorf("hint must NOT surface unbuildable host removed-adapter; got %q", msg)
	}
}

// TestInstall_DefaultAgent_AgentsCliExistingMatch_HintsUmbrella pins
// the umbrella side of the isBuildableAgent contract. When the user
// installed a skill via --agent agents-cli (creating an agents-cli:skill:
// <name> record) and later types `dotpack install <skill>` with no
// --agent (defaults to claude-code), the misroute hint must surface
// --agent agents-cli as the suggested fix — the umbrella IS a buildable
// flag value, distinct from per-host adapters but no less valid.
//
// The earlier "OnlyMatch_NoHint" and "MixedStaleAndBuildableHosts" tests
// pin the negative side (unbuildable hosts are filtered out); this is
// the positive side (umbrellas pass the filter via isBuildableAgent).
// Without the umbrella branch in isBuildableAgent, this test fails with
// "expected hint, got nil" because the record's agent="agents-cli"
// would be silently dropped by the per-host-only adapterFactories
// membership check.
func TestInstall_DefaultAgent_AgentsCliExistingMatch_HintsUmbrella(t *testing.T) {
	setupTriHostEnv(t)
	src := filepath.Join("..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")

	// Seed: install via the umbrella explicitly.
	seed := NewRootCmd()
	seed.SetOut(io_DiscardWriter())
	seed.SetErr(io_DiscardWriter())
	seed.SetArgs([]string{"install", src, "--agent", "agents-cli"})
	if err := seed.Execute(); err != nil {
		t.Fatalf("seed install on agents-cli: %v", err)
	}

	// Trigger: defaulted --agent → claude-code. Hint should surface
	// the umbrella as the buildable suggestion.
	cmd := NewRootCmd()
	cmd.SetOut(io_DiscardWriter())
	cmd.SetErr(io_DiscardWriter())
	cmd.SetArgs([]string{"install", src})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected misroute hint when default --agent and agents-cli record exists, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did you mean") {
		t.Errorf("hint must use 'did you mean' shape; got %q", msg)
	}
	if !strings.Contains(msg, "--agent agents-cli") {
		t.Errorf("hint must name --agent agents-cli (umbrella IS buildable); got %q", msg)
	}
	if !strings.Contains(msg, "dotpack-tracer-bullet") {
		t.Errorf("hint must name the resource by short-name; got %q", msg)
	}

	// Anti-theatre: no claude-code write happened (the misroute
	// short-circuits before orchestrator install).
	claudeSkillPath := filepath.Join(os.Getenv("DOTPACK_CLAUDE_HOME"), "skills", "dotpack-tracer-bullet", "SKILL.md")
	if _, err := os.Stat(claudeSkillPath); err == nil {
		t.Errorf("hint must short-circuit BEFORE writing to claude-code; found %s", claudeSkillPath)
	}
}
