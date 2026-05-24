package resource_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock/dotpack/internal/resource"
)

func TestParseSkill_TracerBulletFixture(t *testing.T) {
	path := filepath.Join("testdata", "skills", "dotpack-tracer-bullet", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	skill, err := resource.ParseSkill(raw)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}

	if skill.Name != "dotpack-tracer-bullet" {
		t.Errorf("Name: got %q, want %q", skill.Name, "dotpack-tracer-bullet")
	}

	if !strings.Contains(skill.Description, "fnord-quasar-blarnacle") {
		t.Errorf("Description should contain trigger phrase 'fnord-quasar-blarnacle'; got %q", skill.Description)
	}

	if skill.License != "" {
		t.Errorf("License: got %q, want empty (fixture has no license field)", skill.License)
	}

	if !strings.Contains(skill.Body, "ECHO-TRACER-BULLET-9F4A2C7B") {
		t.Errorf("Body should contain sentinel string; got %q", skill.Body)
	}

	if len(skill.Extensions) != 0 {
		t.Errorf("Extensions: got %v, want empty (fixture has only universal-core fields)", skill.Extensions)
	}
}

func TestParseSkill_RejectsMissingOpeningDelimiter(t *testing.T) {
	_, err := resource.ParseSkill([]byte("no frontmatter here\n"))
	if err == nil {
		t.Fatal("expected error for input without opening ---")
	}
}

func TestParseSkill_RejectsMissingClosingDelimiter(t *testing.T) {
	raw := []byte("---\nname: foo\ndescription: bar\nno closing delimiter\n")
	_, err := resource.ParseSkill(raw)
	if err == nil {
		t.Fatal("expected error for input without closing ---")
	}
}

func TestParseSkill_CollectsUnknownFieldsAsExtensions(t *testing.T) {
	// allowed-tools is a Claude-only extension per schema/skill.yaml
	// deliberately_excluded.claude_skill_runtime_overrides. ParseSkill
	// must surface it on Extensions so the orchestrator's per-instance
	// lossy detection (ADR-0016 §8) has data to inspect.
	raw := []byte("---\nname: ext-fixture\ndescription: e\nallowed-tools:\n  - Bash\n  - Edit\n---\nbody\n")
	skill, err := resource.ParseSkill(raw)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}
	got, ok := skill.Extensions["allowed-tools"]
	if !ok {
		t.Fatalf("Extensions[allowed-tools] missing; Extensions=%v", skill.Extensions)
	}
	tools, ok := got.([]any)
	if !ok {
		t.Fatalf("Extensions[allowed-tools]: got %T, want []any", got)
	}
	if len(tools) != 2 {
		t.Errorf("len(allowed-tools): got %d, want 2", len(tools))
	}
}
