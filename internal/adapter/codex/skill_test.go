package codex_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/codex"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/schema"
)

func TestCodex_PlanSkill_UserScope_WritesToAgentsHome(t *testing.T) {
	// Codex's ONLY documented native user-scope skill path is
	// AgentsHome/skills/<name>/SKILL.md (per
	// developers.openai.com/codex/skills "$HOME/.agents/skills"). Unlike
	// claude-code and gemini-cli, codex has no host-specific skill root
	// (~/.codex/skills/ is NOT documented by OpenAI). AgentsHome is the
	// host-native path here, not a convergence path the adapter is
	// avoiding — gemini-cli's docstring note about deferring this path to
	// agents-cli does not apply to codex, which has no alternative.
	home := t.TempDir()
	a := codex.New(dirs.Dirs{AgentsHome: home})

	skill := &resource.Skill{
		Name:        "hello-codex",
		Description: "test skill",
		Body:        "instructions go here\n",
	}
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if got := len(plan.Files); got != 1 {
		t.Fatalf("len(plan.Files): got %d, want 1", got)
	}
	want := filepath.Join(home, "skills", "hello-codex", "SKILL.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
	// Skills nest in their owned per-name subdir (SKILL.md + optional
	// scripts/, references/, assets/). TargetDir is the owned subdir so
	// orchestrator.Uninstall reclaims it when empty.
	wantDir := filepath.Join(home, "skills", "hello-codex")
	if plan.TargetDir != wantDir {
		t.Errorf("plan.TargetDir: got %q, want %q", plan.TargetDir, wantDir)
	}
}

func TestCodex_PlanSkill_ProjectScope_WritesUnderProjectDotAgents(t *testing.T) {
	// Project-scope: <ProjectHome>/.agents/skills/<name>/SKILL.md per
	// the OpenAI Codex docs ("$CWD/.agents/skills" and
	// "$REPO_ROOT/.agents/skills"). Same .agents/ convention at both
	// scopes — no .codex/ project equivalent for skills. Path is
	// absolute (slice 2 task #2 invariant — manifest records must
	// survive chdir).
	home := t.TempDir()
	projectHome := t.TempDir()
	a := codex.New(dirs.Dirs{AgentsHome: home, ProjectHome: projectHome})

	skill := &resource.Skill{Name: "h", Description: "d", Body: "b"}
	plan, err := a.Plan(skill, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !filepath.IsAbs(plan.Files[0].Path) {
		t.Errorf("project-scope path %q must be absolute", plan.Files[0].Path)
	}
	want := filepath.Join(projectHome, ".agents", "skills", "h", "SKILL.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
}

func TestCodex_PlanSkill_ProjectScope_NoProjectHome_Errors(t *testing.T) {
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	skill := &resource.Skill{Name: "h", Description: "d", Body: "b"}
	_, err := a.Plan(skill, adapter.ScopeProject)
	if err == nil {
		t.Fatal("expected error when ProjectHome is empty under ScopeProject, got nil")
	}
	if !strings.Contains(err.Error(), "ProjectHome") {
		t.Errorf("error should name the missing field; got %v", err)
	}
}

func TestCodex_PlanSkill_UserScope_NoAgentsHome_Errors(t *testing.T) {
	// Mirror of gemini's NoGeminiHome test — but the missing field's name
	// is AgentsHome (the codex-native root), NOT a hypothetical CodexHome.
	a := codex.New(dirs.Dirs{})
	skill := &resource.Skill{Name: "h", Description: "d", Body: "b"}
	_, err := a.Plan(skill, adapter.ScopeUser)
	if err == nil {
		t.Fatal("expected error when AgentsHome is empty under ScopeUser, got nil")
	}
	if !strings.Contains(err.Error(), "AgentsHome") {
		t.Errorf("error should name the missing field (AgentsHome, not CodexHome); got %v", err)
	}
}

func TestCodex_PlanSkill_FallbackEncodeEmitsFrontmatterAndBody(t *testing.T) {
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	skill := &resource.Skill{
		Name:        "fixture",
		Description: "single-line description",
		Body:        "body content\n",
	}
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := plan.Files[0].Content
	if !bytes.HasPrefix(content, []byte("---\n")) {
		t.Errorf("content must begin with --- delimiter; got %q", string(content))
	}
	if !bytes.Contains(content, []byte("name: fixture")) {
		t.Errorf("content must contain `name: fixture`; got %q", string(content))
	}
	if !bytes.Contains(content, []byte("body content")) {
		t.Errorf("content must contain body verbatim; got %q", string(content))
	}
}

func TestCodex_PlanSkill_BytePerfectPassThroughWithPassThroughMetadataExtensions(t *testing.T) {
	// ADR-0004 byte-identity: if Raw is set AND every extension is one
	// codex KEEPS per §8, content == source bytes. The previous shape of
	// this test (universal-core-only source) short-circuited at
	// `len(extensions) == 0` BEFORE schema.HostKeepsExtension was
	// consulted — pre-#8 hostile-review #1 caught this as theatre. This
	// version sources `keywords` (lossy_when_dropped: false in
	// schema/skill.yaml → codex keeps as pass-through metadata) so the
	// canPassThrough loop actually invokes HostKeepsExtension and
	// exercises the keep-because-pass-through branch in the byte-identity
	// context. If a future change made HostKeepsExtension return false
	// for `keywords` on codex, this test fails because the skill would
	// re-encode and the byte-identity check would break.
	src := []byte("---\nname: passthrough-test\ndescription: d\nkeywords:\n  - tag1\n  - tag2\n---\nbody content\n")
	skill, err := resource.ParseSkill(src)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	// Sanity: the extension was actually parsed — otherwise the test
	// silently degrades to the old "no extensions" case.
	if _, has := skill.Extensions()["keywords"]; !has {
		t.Fatalf("setup: ParseSkill should have surfaced keywords as an extension; got %+v", skill.Extensions())
	}
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !bytes.Equal(plan.Files[0].Content, src) {
		t.Errorf("plan content diverges from source bytes (ADR-0004 violation).\n"+
			"--- source ---\n%s\n--- plan ---\n%s", string(src), string(plan.Files[0].Content))
	}
}

func TestCodex_PlanSkill_DropsClaudeOnlyRuntimeOverrides(t *testing.T) {
	// claude_skill_runtime_overrides (allowed-tools, model, etc.) list
	// ONLY claude-code in aliases — on codex, HostKeepsExtension returns
	// false → the field is NOT emitted in re-encoded frontmatter. (Orchestrator's
	// §8 lossy gate is exercised separately in cli/codex_test.go.) This
	// test exercises the adapter in isolation: with the field present in
	// extensions, the emit must strip it.
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	skill := (&resource.Skill{
		Name:        "claude-only-runtime",
		Description: "d",
		Body:        "b",
	}).WithExtensions(map[string]any{
		"allowed-tools": "Read, Write",
		"model":         "claude-sonnet-4",
	})
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := plan.Files[0].Content
	// Key-form assertion — `Contains("allowed-tools")` would
	// false-positive against any body or description containing the word.
	// (This bit a CLI test earlier in this slice; same hazard avoided
	// here.)
	if bytes.Contains(content, []byte("\nallowed-tools:")) {
		t.Errorf("codex must NOT emit claude-only allowed-tools key; got:\n%s", string(content))
	}
	if bytes.Contains(content, []byte("\nmodel:")) {
		t.Errorf("codex must NOT emit claude-only model key; got:\n%s", string(content))
	}
}

func TestCodex_PlanSkill_PreservesPassThroughMetadata(t *testing.T) {
	// `keywords` and `metadata` are lossy_when_dropped: false in
	// schema/skill.yaml — pass-through bins that no host parses but the
	// adapter must round-trip. schema.HostKeepsExtension must return true
	// for these on every host (the consolidated rule shared with
	// claudecode and gemini).
	a := codex.New(dirs.Dirs{AgentsHome: t.TempDir()})
	skill := (&resource.Skill{
		Name:        "with-keywords",
		Description: "d",
		Body:        "b",
	}).WithExtensions(map[string]any{
		"keywords": []string{"testing", "yaml"},
	})
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := plan.Files[0].Content
	if !bytes.Contains(content, []byte("\nkeywords:")) {
		t.Errorf("codex must preserve pass-through `keywords` key; got:\n%s", string(content))
	}
}

func TestSchemaLossy_AllowedToolsOnCodex_Lossy(t *testing.T) {
	// Triangulating §8 across hosts: the discriminating field
	// `allowed-tools` is claude-code-only per
	// claude_skill_runtime_overrides aliases. On codex (third real
	// adapter host), it must classify lossy with no per-host branching in
	// LossyExtensions. Same shape as the gemini-cli version of this test
	// in schema/lossy_test.go.
	reasons, err := schema.LossyExtensions(resource.KindSkill, "codex", map[string]any{
		"allowed-tools": "Read, Write",
	})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(reasons) != 1 {
		t.Fatalf("len(reasons): got %d, want 1; reasons=%v", len(reasons), reasons)
	}
	if reasons[0].FieldPath != "allowed-tools" {
		t.Errorf("reasons[0].FieldPath: got %q, want %q", reasons[0].FieldPath, "allowed-tools")
	}
}
