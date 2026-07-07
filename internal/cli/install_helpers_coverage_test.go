package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
)

func TestResolveKindAndNameHelpers(t *testing.T) {
	for _, explicit := range []string{"skill", "agent", "mcp-server", "hook", "rule", "command", "memory"} {
		if got, err := resolveKind(explicit, "whatever"); err != nil || string(got) != explicit {
			t.Fatalf("resolveKind(%q) = %q,%v", explicit, got, err)
		}
	}
	if _, err := resolveKind("bad", "whatever"); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("bad explicit kind err=%v", err)
	}
	cases := []struct {
		path string
		kind resource.Kind
	}{
		{filepath.Join(".agents", "rules", "rule.md"), resource.KindRule},
		{filepath.Join(".agents", "commands", "cmd.toml"), resource.KindCommand},
		{"CLAUDE.md", resource.KindMemory},
		{"HERMES.md", resource.KindMemory},
		{".hermes.md", resource.KindMemory},
		{"SOUL.md", resource.KindMemory},
	}
	for _, tc := range cases {
		got, err := resolveKind("", tc.path)
		if err != nil || got != tc.kind {
			t.Fatalf("resolveKind(%s) = %q,%v; want %q", tc.path, got, err, tc.kind)
		}
	}
	if _, err := resolveKind("", "unknown.md"); err == nil || !strings.Contains(err.Error(), "cannot infer") {
		t.Fatalf("unknown inference err=%v", err)
	}

	for _, source := range []string{"guard.hook.json", "guard.hook.yaml", "guard.hook.yml", "guard.json", "guard.yaml", "guard.yml", "guard.hook", "guard"} {
		if got := hookNameFromPath(source); got != "guard" {
			t.Fatalf("hookNameFromPath(%q)=%q; want guard", source, got)
		}
	}
	if got := commandNameFromPath("deploy.toml"); got != "deploy" {
		t.Fatalf("commandNameFromPath = %q; want deploy", got)
	}
	if _, err := parseScope("bogus"); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("parseScope invalid err=%v", err)
	}
}

func TestLoadResourceCoversKindsAndSupportFileErrors(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "skills", "s")
	mustWriteTestFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: s\ndescription: d\n---\nb\n")
	mustWriteTestFile(t, filepath.Join(skillDir, "references", "r.md"), "ref\n")
	res, err := loadResource(resource.KindSkill, filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("load skill: %v", err)
	}
	skill := res.(*resource.Skill)
	if len(skill.SupportFiles) != 1 || skill.SupportFiles[0].RelPath != "references/r.md" {
		t.Fatalf("support files = %+v", skill.SupportFiles)
	}
	if err := os.Symlink(filepath.Join(skillDir, "references", "r.md"), filepath.Join(skillDir, "references", "link.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := loadResource(resource.KindSkill, filepath.Join(skillDir, "SKILL.md")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("load skill with symlink err=%v; want symlink", err)
	}
	if _, err := loadSkillSupportFiles(filepath.Join(tmp, "missing-skill", "SKILL.md")); err == nil {
		t.Fatal("loadSkillSupportFiles missing skill directory expected error")
	}

	fixtures := map[resource.Kind]string{
		resource.KindAgent:     "---\nname: a\ndescription: d\n---\nbody\n",
		resource.KindMCPServer: `{"mcpServers":{"github":{"command":"npx","args":[]}}}`,
		resource.KindHook:      `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"/bin/true"}]}]}}`,
		resource.KindRule:      "---\nname: r\nartifact-type: rule\n---\nbody\n",
		resource.KindCommand:   "---\ndescription: d\n---\nbody\n",
		resource.KindMemory:    "remember\n",
	}
	for kind, raw := range fixtures {
		name := strings.ReplaceAll(string(kind), "-", "_")
		ext := ".md"
		if kind == resource.KindMCPServer || kind == resource.KindHook {
			ext = ".json"
		}
		if kind == resource.KindCommand {
			name = "cmd"
		}
		path := filepath.Join(tmp, name+ext)
		if kind == resource.KindMemory {
			path = filepath.Join(tmp, "AGENTS.md")
		}
		mustWriteTestFile(t, path, raw)
		if _, err := loadResource(kind, path); err != nil {
			t.Fatalf("loadResource(%s): %v", kind, err)
		}
	}
	if _, err := loadResource(resource.Kind("unknown"), filepath.Join(tmp, "AGENTS.md")); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("loadResource unknown err=%v", err)
	}
	invalidAgent := filepath.Join(tmp, "invalid-agent.md")
	mustWriteTestFile(t, invalidAgent, "---\nname: Bad Name\n---\n")
	if _, err := loadResource(resource.KindAgent, invalidAgent); err == nil || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("loadResource invalid agent err=%v; want validation", err)
	}

	invalidSkill := filepath.Join(tmp, "bad-skill", "SKILL.md")
	mustWriteTestFile(t, invalidSkill, "---\nname: Bad Name\ndescription: d\n---\nbody\n")
	if _, err := loadResource(resource.KindSkill, invalidSkill); err == nil || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("loadResource invalid skill err=%v; want validation", err)
	}
	badHook := filepath.Join(tmp, "bad-hook.json")
	mustWriteTestFile(t, badHook, "{bad")
	if _, err := loadResource(resource.KindHook, badHook); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("loadResource bad hook err=%v; want parse", err)
	}
	badRule := filepath.Join(tmp, "bad-rule.md")
	mustWriteTestFile(t, badRule, "not frontmatter\n")
	if _, err := loadResource(resource.KindRule, badRule); err == nil || !strings.Contains(err.Error(), "opening") {
		t.Fatalf("loadResource bad rule err=%v; want opening delimiter", err)
	}
	invalidCommand := filepath.Join(tmp, "invalid-command.md")
	mustWriteTestFile(t, invalidCommand, "---\ndescription: d\n---\n")
	if _, err := loadResource(resource.KindCommand, invalidCommand); err == nil || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("loadResource invalid command err=%v; want validation", err)
	}
}

func TestBuildAdaptersUmbrellaAndBuildableHelpers(t *testing.T) {
	d := dirs.Dirs{
		ClaudeHome:      t.TempDir(),
		GeminiHome:      t.TempDir(),
		AntigravityHome: t.TempDir(),
		AgentsHome:      t.TempDir(),
		CodexHome:       t.TempDir(),
		HermesHome:      t.TempDir(),
		ProjectHome:     t.TempDir(),
	}
	if _, err := buildAdapter("claude-code", d); err != nil {
		t.Fatalf("buildAdapter claude-code: %v", err)
	}
	if _, err := buildAdapter("hermes", d); err != nil {
		t.Fatalf("buildAdapter hermes: %v", err)
	}
	if _, err := buildAdapter("unknown", d); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("buildAdapter unknown err=%v", err)
	}
	subs, writers, err := buildUmbrella("agents-cli", d)
	if err != nil {
		t.Fatalf("buildUmbrella agents-cli: %v", err)
	}
	if len(subs) != 3 || len(writers[resource.KindSkill]) != 1 {
		t.Fatalf("umbrella subs/writers = %d/%+v", len(subs), writers)
	}
	if _, _, err := buildUmbrella("missing", d); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("buildUmbrella missing err=%v", err)
	}
	if !isBuildableAgent("agents-cli") || !isBuildableAgent("codex") || isBuildableAgent("missing") {
		t.Fatalf("isBuildableAgent returned unexpected values")
	}
}

func TestInstallCanonicalEntryUnsupportedAndPlanErrors(t *testing.T) {
	agentsHome, dotpackHome := setupCodexEnv(t)
	d := dirs.Dirs{AgentsHome: agentsHome, CodexHome: t.TempDir(), DotpackHome: dotpackHome, ProjectHome: t.TempDir()}
	store := manifestStoreForTest(t, dotpackHome)
	entry := canonicalEntry{Kind: resource.KindAgent, Path: "/tmp/a.md", Resource: &resource.Agent{Name: "a", Description: "d", Body: "b"}}
	if _, unsupported, err := installCanonicalEntry(entry, "gemini-cli", adapter.ScopeUser, false, false, "", "", d, store); err == nil || unsupported {
		t.Fatalf("installCanonicalEntry missing GeminiHome err=%v unsupported=%v; want error", err, unsupported)
	}
	// memory is now a first-class fan-out kind under agents-cli (ADR-0014):
	// plansForEntry yields one plan per sub-adapter (GEMINI.md /
	// ANTIGRAVITY.md / AGENTS.md), none unsupported, given the host homes.
	full := dirs.Dirs{
		AgentsHome:      agentsHome,
		CodexHome:       d.CodexHome,
		GeminiHome:      t.TempDir(),
		AntigravityHome: t.TempDir(),
		DotpackHome:     dotpackHome,
		ProjectHome:     d.ProjectHome,
	}
	entry = canonicalEntry{Kind: resource.KindMemory, Path: "/tmp/AGENTS.md", Resource: &resource.Memory{Name: "AGENTS.md", Body: "b"}}
	plans, unsupported, err := plansForEntry(entry, "agents-cli", adapter.ScopeUser, full)
	if err != nil || unsupported {
		t.Fatalf("plansForEntry memory = unsupported %v err %v; want supported fan-out", unsupported, err)
	}
	if len(plans) != 3 {
		t.Fatalf("plansForEntry memory plans = %d; want 3 (fan-out across sub-adapters)", len(plans))
	}
}
