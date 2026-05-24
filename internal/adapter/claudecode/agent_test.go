package claudecode_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/claudecode"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
)

func TestClaudeCode_CapabilitiesAgentIsNative(t *testing.T) {
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	caps := a.Capabilities()
	if got := caps[resource.KindAgent]; got != adapter.Native {
		t.Errorf("Capabilities[agent]: got %v, want Native", got)
	}
}

func TestClaudeCode_PlanAgent_UserScope_FlatFileLayout(t *testing.T) {
	// Agents are a FLAT file <root>/agents/<name>.md, NOT nested in a
	// per-name subdirectory the way skills are. The plan.TargetDir must
	// be empty so orchestrator.Uninstall does NOT call os.Remove on the
	// shared <root>/agents/ directory.
	home := t.TempDir()
	a := claudecode.New(dirs.Dirs{ClaudeHome: home})

	ag := &resource.Agent{Name: "hello-agent", Description: "d", Body: "b"}
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := len(plan.Files); got != 1 {
		t.Fatalf("len(plan.Files): got %d, want 1", got)
	}
	want := filepath.Join(home, "agents", "hello-agent.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
	if plan.TargetDir != "" {
		t.Errorf("plan.TargetDir: got %q, want empty (agents/ is shared, not owned)", plan.TargetDir)
	}
}

func TestClaudeCode_PlanAgent_ProjectScope_WritesUnderProjectClaude(t *testing.T) {
	projectHome := t.TempDir()
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir(), ProjectHome: projectHome})
	ag := &resource.Agent{Name: "h", Description: "d", Body: "b"}
	plan, err := a.Plan(ag, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := filepath.Join(projectHome, ".claude", "agents", "h.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
}

func TestClaudeCode_PlanAgent_ProjectScope_NoProjectHome_Errors(t *testing.T) {
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	ag := &resource.Agent{Name: "h", Description: "d", Body: "b"}
	if _, err := a.Plan(ag, adapter.ScopeProject); err == nil {
		t.Fatal("expected error when ProjectHome is empty under ScopeProject, got nil")
	}
}

func TestClaudeCode_PlanAgent_EmitsFrontmatterUniversalCoreAndBody(t *testing.T) {
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	ag := &resource.Agent{
		Name:        "fixture-agent",
		Description: "trigger words here",
		Model:       "sonnet",
		Tools:       []string{"Read", "Write", "Edit"},
		Body:        "system prompt body\n",
	}
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := plan.Files[0].Content
	if !bytes.HasPrefix(content, []byte("---\n")) {
		t.Errorf("content must begin with --- delimiter; got %q", string(content))
	}
	if !bytes.Contains(content, []byte("name: fixture-agent")) {
		t.Errorf("content must contain name; got %q", string(content))
	}
	if !bytes.Contains(content, []byte("description: trigger words here")) {
		t.Errorf("content must contain description; got %q", string(content))
	}
	if !bytes.Contains(content, []byte("model: sonnet")) {
		t.Errorf("content must contain model; got %q", string(content))
	}
	// Claude convention: tools as comma-separated string (5/5 corpus
	// presence per schema/agent.yaml). Even when source was a YAML array,
	// the adapter re-emits in the host's preferred shape.
	if !bytes.Contains(content, []byte("tools: Read, Write, Edit")) {
		t.Errorf("content must contain tools as comma-separated string; got %q", string(content))
	}
	if !bytes.Contains(content, []byte("system prompt body")) {
		t.Errorf("content must contain body; got %q", string(content))
	}
}

func TestClaudeCode_PlanAgent_AlwaysReEncodes_TollsNormalisedToClaudeShape(t *testing.T) {
	// Even when Raw bytes are present (Agent parsed from source), planAgent
	// must re-encode so tools is always in claude-code's preferred shape
	// (comma-separated string). Pass-through would ship YAML-array form
	// to claude-code for a gemini-shaped source, and claude-code's loader
	// is not corpus-verified to parse YAML arrays for tools — install
	// would silently succeed but the agent would have no tools at runtime.
	raw := []byte("---\nname: re-enc\ndescription: d\ntools:\n  - read_file\n  - grep_search\n---\nbody\n")
	ag, err := resource.ParseAgent(raw)
	if err != nil {
		t.Fatalf("ParseAgent: %v", err)
	}
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if bytes.Equal(plan.Files[0].Content, raw) {
		t.Errorf("planAgent must re-encode, not pass through Raw bytes (tools form coercion)")
	}
	if !bytes.Contains(plan.Files[0].Content, []byte("tools: read_file, grep_search")) {
		t.Errorf("tools must be re-emitted as comma-separated string; got %q", string(plan.Files[0].Content))
	}
}

func TestClaudeCode_PlanAgent_PreservesClaudeSubagentRuntimeOverrides(t *testing.T) {
	// claude_subagent_runtime_overrides (maxTurns, disallowedTools, etc.)
	// list claude-code in their aliases — they must round-trip into the
	// emitted frontmatter, NOT be silently dropped.
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	ag := (&resource.Agent{
		Name:        "with-overrides",
		Description: "d",
		Body:        "b",
	}).WithExtensions(map[string]any{
		"maxTurns":       5,
		"permissionMode": "ask",
	})
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := plan.Files[0].Content
	if !bytes.Contains(content, []byte("maxTurns")) {
		t.Errorf("content must preserve maxTurns; got %q", string(content))
	}
	if !bytes.Contains(content, []byte("permissionMode")) {
		t.Errorf("content must preserve permissionMode; got %q", string(content))
	}
}

func TestClaudeCode_PlanSkill_StillSetsTargetDirToOwnedSubdir(t *testing.T) {
	// Regression: planSkill must continue to populate plan.TargetDir
	// with the per-skill owned subdir so orchestrator.Uninstall can
	// reclaim it when empty. Test pins the new contract (was implicit
	// in buildRecord's filepath.Dir(plan.Files[0].Path) heuristic before).
	home := t.TempDir()
	a := claudecode.New(dirs.Dirs{ClaudeHome: home})
	skill := &resource.Skill{Name: "s", Description: "d", Body: "b"}
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := filepath.Join(home, "skills", "s")
	if plan.TargetDir != want {
		t.Errorf("plan.TargetDir: got %q, want %q", plan.TargetDir, want)
	}
}

func TestSchemaLossy_AgentTemperatureLossyOnClaudeCode(t *testing.T) {
	// Discriminating test (per advisor): `temperature: 0.5` is bound to
	// gemini_agent_runtime_overrides (gemini-cli only). Computing lossy
	// against claude-code must classify it as lossy. Different concept
	// than skill's allowed-tools, same §8 code path — pins that the
	// schema-driven design generalises across kinds with no per-kind
	// branching in the algorithm.
	reasons, err := schema.LossyExtensions(resource.KindAgent, "claude-code", map[string]any{
		"temperature": 0.5,
	})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(reasons) != 1 {
		t.Fatalf("len(reasons): got %d, want 1; reasons=%v", len(reasons), reasons)
	}
	if reasons[0].FieldPath != "temperature" {
		t.Errorf("reasons[0].FieldPath: got %q, want %q", reasons[0].FieldPath, "temperature")
	}
	if !strings.Contains(reasons[0].CanonicalConcept, "gemini_agent_runtime_overrides") {
		t.Errorf("reasons[0].CanonicalConcept: got %q, want gemini_agent_runtime_overrides", reasons[0].CanonicalConcept)
	}
}
