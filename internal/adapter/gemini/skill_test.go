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
)

func TestGemini_PlanSkill_UserScope_WritesToGeminiHome(t *testing.T) {
	// User-scope native path is <GeminiHome>/skills/<name>/SKILL.md.
	// Same on-disk layout as claude-code (skills nest in their own dir);
	// only the root differs (.gemini vs .claude). The shared
	// ~/.agents/skills/ convergence path is reserved for agents-cli
	// (ADR-0016 §1) and explicitly NOT claimed here.
	home := t.TempDir()
	a := gemini.New(dirs.Dirs{GeminiHome: home})

	skill := &resource.Skill{
		Name:        "hello-gemini",
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
	want := filepath.Join(home, "skills", "hello-gemini", "SKILL.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
	// Skills own their per-name subdir on gemini-cli too — TargetDir
	// must be the owned <root>/skills/<name>/ so orchestrator.Uninstall
	// can reclaim it when empty.
	wantDir := filepath.Join(home, "skills", "hello-gemini")
	if plan.TargetDir != wantDir {
		t.Errorf("plan.TargetDir: got %q, want %q", plan.TargetDir, wantDir)
	}
}

func TestGemini_PlanSkill_ProjectScope_WritesUnderProjectGemini(t *testing.T) {
	// Project-scope mirrors claudecode's <ProjectHome>/.claude/... shape,
	// but rooted at .gemini instead. Path is absolute (slice 2 task #2
	// invariant — manifest records must survive chdir).
	home := t.TempDir()
	projectHome := t.TempDir()
	a := gemini.New(dirs.Dirs{GeminiHome: home, ProjectHome: projectHome})

	skill := &resource.Skill{Name: "h", Description: "d", Body: "b"}
	plan, err := a.Plan(skill, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !filepath.IsAbs(plan.Files[0].Path) {
		t.Errorf("project-scope path %q must be absolute", plan.Files[0].Path)
	}
	want := filepath.Join(projectHome, ".gemini", "skills", "h", "SKILL.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}
}

func TestGemini_PlanSkill_ProjectScope_NoProjectHome_Errors(t *testing.T) {
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir()})
	skill := &resource.Skill{Name: "h", Description: "d", Body: "b"}
	_, err := a.Plan(skill, adapter.ScopeProject)
	if err == nil {
		t.Fatal("expected error when ProjectHome is empty under ScopeProject, got nil")
	}
	if !strings.Contains(err.Error(), "ProjectHome") {
		t.Errorf("error should name the missing field; got %v", err)
	}
}

func TestGemini_PlanSkill_UserScope_NoGeminiHome_Errors(t *testing.T) {
	a := gemini.New(dirs.Dirs{})
	skill := &resource.Skill{Name: "h", Description: "d", Body: "b"}
	_, err := a.Plan(skill, adapter.ScopeUser)
	if err == nil {
		t.Fatal("expected error when GeminiHome is empty under ScopeUser, got nil")
	}
	if !strings.Contains(err.Error(), "GeminiHome") {
		t.Errorf("error should name the missing field; got %v", err)
	}
}

func TestGemini_PlanSkill_FallbackEncodeEmitsFrontmatterAndBody(t *testing.T) {
	// Synthesised skill (no Raw) → re-encode fallback. Same shape as
	// claudecode's fallback test: assert delimiter, name, body.
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir()})
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

func TestGemini_PlanSkill_BytePerfectPassThroughWhenNoLossyExtensions(t *testing.T) {
	// ADR-0008 byte-identity guarantee applies here too: if ParseSkill
	// captured Raw bytes and no extension would be dropped on gemini-cli,
	// plan.Files[0].Content MUST equal the source bytes. (For gemini-cli,
	// even fewer extensions are "kept" than claude-code, so most
	// real-world skills with extensions will re-encode — but a skill with
	// ONLY universal core + pass-through-metadata keys still pass through.)
	src := []byte("---\nname: passthrough-test\ndescription: d\n---\nbody content\n")
	skill, err := resource.ParseSkill(src)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	a := gemini.New(dirs.Dirs{GeminiHome: t.TempDir()})
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !bytes.Equal(plan.Files[0].Content, src) {
		t.Errorf("plan content diverges from source bytes (ADR-0008 violation).\n"+
			"--- source ---\n%s\n--- plan ---\n%s", string(src), string(plan.Files[0].Content))
	}
}
