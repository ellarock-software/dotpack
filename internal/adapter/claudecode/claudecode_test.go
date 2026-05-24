package claudecode_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/claudecode"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

func TestClaudeCode_HostID(t *testing.T) {
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	if got := a.HostID(); got != "claude-code" {
		t.Errorf("HostID: got %q, want %q", got, "claude-code")
	}
}

func TestClaudeCode_CapabilitiesSkillIsNative(t *testing.T) {
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	caps := a.Capabilities()
	if got := caps[resource.KindSkill]; got != adapter.Native {
		t.Errorf("Capabilities[skill]: got %v, want Native (per ADR-0009)", got)
	}
}

func TestClaudeCode_PlanSkill_UserScope_WritesToClaudeHome(t *testing.T) {
	home := t.TempDir()
	a := claudecode.New(dirs.Dirs{ClaudeHome: home})

	skill := &resource.Skill{
		Name:        "hello-world",
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
	want := filepath.Join(home, "skills", "hello-world", "SKILL.md")
	if plan.Files[0].Path != want {
		t.Errorf("plan.Files[0].Path: got %q, want %q", plan.Files[0].Path, want)
	}

	if len(plan.MergedKeys) != 0 {
		t.Errorf("plan.MergedKeys: got %d, want 0 (skill is a drop-file kind, not config-merge)", len(plan.MergedKeys))
	}
}

func TestClaudeCode_PlanSkill_ProjectScope_WritesToProjectClaudeDir(t *testing.T) {
	// ScopeProject targets ./.claude/skills/<name>/SKILL.md per ADR-0009.
	// The base for "./" is the CWD where dotpack ran. We don't override
	// CWD here — just check the path is relative-to-CWD-shaped (no
	// leading dirs.ClaudeHome prefix).
	home := t.TempDir()
	a := claudecode.New(dirs.Dirs{ClaudeHome: home})

	skill := &resource.Skill{Name: "h", Description: "d", Body: "b"}
	plan, err := a.Plan(skill, adapter.ScopeProject)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("len(plan.Files): got %d, want 1", len(plan.Files))
	}
	// project-scope path must NOT live under ClaudeHome
	if filepath.HasPrefix(plan.Files[0].Path, home) {
		t.Errorf("project-scope path %q should not be under ClaudeHome %q", plan.Files[0].Path, home)
	}
	// must end with .claude/skills/h/SKILL.md
	tail := filepath.Join(".claude", "skills", "h", "SKILL.md")
	if !endsWith(plan.Files[0].Path, tail) {
		t.Errorf("plan.Files[0].Path %q should end with %q", plan.Files[0].Path, tail)
	}
}

func TestClaudeCode_PlanSkill_FallbackEncodeEmitsFrontmatterAndBody(t *testing.T) {
	// When a Skill was synthesised (no Raw bytes captured by ParseSkill),
	// the adapter falls back to re-encoding the universal core. Pin the
	// fallback's output shape so the host-verification probe has a
	// stable target if it ever exercises this path. Re-encoding is NOT
	// the production code path for installed-from-source resources —
	// see TestClaudeCode_PlanSkill_BytePerfectPassThroughOfParsedSource
	// below for the ADR-0008 guarantee.
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	skill := &resource.Skill{
		Name:        "fixture",
		Description: "single-line description",
		Body:        "body content\n",
		// no Raw → triggers fallback re-encode
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

func TestClaudeCode_PlanSkill_BytePerfectPassThroughOfParsedSource(t *testing.T) {
	// Per ADR-0008: installed drop-file resources are byte-identical to
	// their cache copy. When ParseSkill captured Raw bytes and no
	// Extensions need re-encoding, plan.Files[0].Content MUST equal
	// the source bytes. Authorial formatting (folded scalars, comments,
	// key order) survives — yaml.Marshal would have normalised it away.
	src, err := os.ReadFile(filepath.Join("..", "..", "resource", "testdata", "skills", "dotpack-tracer-bullet", "SKILL.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	skill, err := resource.ParseSkill(src)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	a := claudecode.New(dirs.Dirs{ClaudeHome: t.TempDir()})
	plan, err := a.Plan(skill, adapter.ScopeUser)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !bytes.Equal(plan.Files[0].Content, src) {
		t.Errorf("plan content diverges from source bytes (ADR-0008 violation).\n"+
			"--- source ---\n%s\n--- plan ---\n%s", string(src), string(plan.Files[0].Content))
	}
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
