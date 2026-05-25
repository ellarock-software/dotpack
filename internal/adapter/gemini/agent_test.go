package gemini_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/gemini"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
)

func TestGemini_PlanAgent_UserScope_FlatFileLayout(t *testing.T) {
	// Same flat-layout invariant as claudecode: <root>/agents/<name>.md,
	// NOT nested. TargetDir empty so orchestrator.Uninstall does NOT
	// reclaim the shared <root>/agents/ dir.
	home := t.TempDir()
	a := gemini.New(dirs.Dirs{GeminiHome: home})

	ag := &resource.Agent{Name: "hello-agent", Description: "d", Body: "b"}
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := filepath.Join(home, "agents", "hello-agent.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
	if plan.TargetDir != "" {
		t.Errorf("plan.TargetDir: got %q, want empty (agents/ is shared)", plan.TargetDir)
	}
}

func TestGemini_PlanAgent_ProjectScope_WritesUnderProjectGemini(t *testing.T) {
	projectHome := t.TempDir()
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir(), ProjectHome: projectHome})
	ag := &resource.Agent{Name: "h", Description: "d", Body: "b"}
	plan, err := a.Plan(ag, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	want := filepath.Join(projectHome, ".gemini", "agents", "h.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
}

func TestGemini_PlanAgent_EmitsToolsAsYAMLArray_InverseOfClaudeCode(t *testing.T) {
	// DISCRIMINATING TEST 3 (per advisor): a Claude-shaped source
	// (tools as comma-string) installed onto gemini-cli must emit tools
	// in gemini-cli's preferred shape (YAML array). This is the inverse
	// of claudecode's coercion direction — and the test that says the
	// "always re-encode for one kind" pattern is per-(host, kind), not
	// per-kind. If a future shared base accidentally hard-codes one
	// direction, this fails.
	//
	// Assert BOTH directions: presence-of-array AND absence-of-comma-string.
	raw := []byte("---\nname: re-enc\ndescription: d\ntools: Read, Write, Edit\n---\nbody\n")
	ag, err := resource.ParseAgent(raw)
	if err != nil {
		t.Fatalf("ParseAgent: %v", err)
	}
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir()})
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := string(plan.Files[0].Content)

	// Presence-of-array. YAML marshal of []string{"Read","Write","Edit"}
	// produces an indented sequence; allow either standard 4-space or
	// 2-space indent (yaml.v3 default is 4 for nested under a mapping).
	hasArrayForm := strings.Contains(content, "tools:\n    - Read") ||
		strings.Contains(content, "tools:\n  - Read")
	if !hasArrayForm {
		t.Errorf("gemini-cli must emit tools as YAML array (inverse of claudecode); got:\n%s", content)
	}

	// Absence-of-comma-string. If the emit silently fell back to
	// claude's "tools: Read, Write, Edit" form (e.g. via a copy-pasted
	// claudecode helper), this catches it.
	if strings.Contains(content, "tools: Read, Write, Edit") {
		t.Errorf("gemini-cli must NOT emit tools as comma-string; got:\n%s", content)
	}
}

func TestGemini_PlanAgent_EmitsUniversalCoreAndBody(t *testing.T) {
	// Universal-core emit shape: name, description, model, tools, body
	// all round-trip. (Tools shape is discriminated separately above;
	// here we just confirm presence.)
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir()})
	ag := &resource.Agent{
		Name:        "fixture-gemini-agent",
		Description: "trigger words here",
		Model:       "gemini-2.0-flash",
		Tools:       []string{"read_file", "grep_search"},
		Body:        "system prompt body\n",
	}
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := plan.Files[0].Content
	for _, want := range []string{
		"name: fixture-gemini-agent",
		"description: trigger words here",
		"model: gemini-2.0-flash",
		"system prompt body",
	} {
		if !bytes.Contains(content, []byte(want)) {
			t.Errorf("emitted content must contain %q; got:\n%s", want, string(content))
		}
	}
}

func TestGemini_PlanAgent_PreservesGeminiAgentRuntimeOverrides(t *testing.T) {
	// gemini_agent_runtime_overrides (temperature, max_turns, kind,
	// timeout_mins) list gemini-cli in their aliases — they must
	// round-trip into the emitted frontmatter, NOT be silently dropped.
	// Inverse counterpart of TestClaudeCode_PlanAgent_PreservesClaudeSubagentRuntimeOverrides.
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir()})
	ag := (&resource.Agent{
		Name:        "with-gemini-overrides",
		Description: "d",
		Body:        "b",
	}).WithExtensions(map[string]any{
		"temperature":  0.5,
		"max_turns":    8,
		"timeout_mins": 30,
	})
	plan, err := a.Plan(ag, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := plan.Files[0].Content
	// Key-form assertion (`\nfieldname:`) — substring `temperature`
	// would false-positive against any body or description text that
	// happens to contain the word. This bit a CLI test earlier in this
	// slice (the `allowed-tools` description fixture); preventing the
	// same hazard here.
	for _, want := range []string{"temperature", "max_turns", "timeout_mins"} {
		needle := []byte("\n" + want + ":")
		if !bytes.Contains(content, needle) {
			t.Errorf("content must preserve %q field (gemini_agent_runtime_overrides); got:\n%s",
				want, string(content))
		}
	}
}

func TestGemini_PlanAgent_DropsClaudeOnlyOverrides_ButThatsLossyAtOrchestrator(t *testing.T) {
	// claude_subagent_runtime_overrides (maxTurns, disallowedTools, etc.)
	// list ONLY claude-code in aliases. On gemini-cli,
	// schema.HostKeepsExtension("gemini-cli", ...) returns false → the
	// field is NOT emitted in re-encoded frontmatter.
	// At the orchestrator layer, the §8 lossy check would refuse the
	// install unless --allow-lossy. This test exercises the adapter in
	// isolation: with the field present in extensions, the emit is
	// stripped (the orchestrator's lossy gate is a separate concern).
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir()})
	ag := (&resource.Agent{
		Name:        "claude-only-runtime",
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
	// Key-form assertion — `Contains("maxTurns")` would false-positive
	// against any body or description containing the word. The fields
	// must be absent as KEYS in the frontmatter, not absent as
	// substrings.
	if bytes.Contains(content, []byte("\nmaxTurns:")) {
		t.Errorf("gemini-cli must NOT emit claude-only maxTurns key in re-encoded frontmatter; got:\n%s", string(content))
	}
	if bytes.Contains(content, []byte("\npermissionMode:")) {
		t.Errorf("gemini-cli must NOT emit claude-only permissionMode key in re-encoded frontmatter; got:\n%s", string(content))
	}
}

func TestSchemaLossy_AgentTemperatureNotLossyOnGeminiCLI_PositiveControl(t *testing.T) {
	// DISCRIMINATING TEST 2 (per advisor): the existing claudecode test
	// (TestSchemaLossy_AgentTemperatureLossyOnClaudeCode) asserts that
	// `temperature: 0.5` is LOSSY on claude-code. This is the INVERSE
	// positive control: on gemini-cli (where gemini_agent_runtime_overrides
	// lists gemini-cli in aliases), `temperature` is NOT lossy. The
	// schema-driven §8 algorithm must classify the same field
	// differently on the two real adapter HostIDs with NO per-host
	// branching in the algorithm.
	reasons, err := schema.LossyExtensions(resource.KindAgent, "gemini-cli", map[string]any{
		"temperature": 0.5,
	})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(reasons) != 0 {
		t.Errorf("len(reasons) on gemini-cli: got %d, want 0 (temperature is gemini-native); reasons=%v",
			len(reasons), reasons)
	}
}
