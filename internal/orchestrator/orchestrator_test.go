package orchestrator_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/claudecode"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/manifest"
	"github.com/ellarock/dotpack/internal/orchestrator"
	"github.com/ellarock/dotpack/internal/resource"
)

func TestInstall_SkillToClaudeCode_WritesFileAndRecordsManifest(t *testing.T) {
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{
		Name:        "hello-world",
		Description: "test skill",
		Body:        "instructions\n",
	}
	res, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "file:///fake/SKILL.md"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	want := filepath.Join(d.ClaudeHome, "skills", "hello-world", "SKILL.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected SKILL.md at %s: %v", want, err)
	}
	if len(res.Plan.Files) != 1 || res.Plan.Files[0].Path != want {
		t.Errorf("result Plan.Files mismatch: %+v", res.Plan.Files)
	}
	if res.Record.ID == "" || res.Record.Agent != "claude-code" || res.Record.Kind != "skill" {
		t.Errorf("Record fields incomplete: %+v", res.Record)
	}

	loaded, err := mf.Load()
	if err != nil {
		t.Fatalf("Load manifest: %v", err)
	}
	if len(loaded.Installs) != 1 {
		t.Fatalf("manifest len: got %d, want 1", len(loaded.Installs))
	}
	if loaded.Installs[0].ID != res.Record.ID {
		t.Errorf("manifest record mismatch: got %q, want %q", loaded.Installs[0].ID, res.Record.ID)
	}
}

func TestInstall_FileContentMatchesPlan(t *testing.T) {
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{Name: "x", Description: "d", Body: "b\n"}
	res, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(res.Plan.Files[0].Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(res.Plan.Files[0].Content) {
		t.Errorf("on-disk content != plan content")
	}
}

func TestInstall_UnknownExtensionRefusedWithoutAllowLossy(t *testing.T) {
	// An extension with no matching canonical_concept in schema/skill.yaml
	// is treated as lossy by default (ADR-0016 §8 failure-mode-safety:
	// silent drop is worse than loud block). `made_up_field` is not
	// listed under deliberately_excluded → no host claims to support it →
	// install refused without --allow-lossy.
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{
		Name: "y", Description: "d", Body: "b",
		Extensions: map[string]any{"made_up_field": "foo"},
	}
	_, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err == nil {
		t.Fatal("expected lossy refusal, got nil")
	}
	var le *orchestrator.LossyError
	if !errors.As(err, &le) {
		t.Fatalf("error: got %T (%v), want *orchestrator.LossyError", err, err)
	}
	if len(le.Reasons) == 0 || le.Reasons[0].FieldPath != "made_up_field" {
		t.Errorf("LossyError.Reasons: got %+v, want one entry for made_up_field", le.Reasons)
	}
}

func TestInstall_UnknownExtensionProceedsWithAllowLossy(t *testing.T) {
	// Slice-2 successor to the slice-1 "lossy proceeds with flag" test:
	// the result surfaces the dropped fields (LossyReasons on
	// InstallResult, not on the now-removed plan.Lossy).
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{
		Name: "z", Description: "d", Body: "b",
		Extensions: map[string]any{"made_up_field": "foo"},
	}
	res, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f", AllowLossy: true})
	if err != nil {
		t.Fatalf("Install with AllowLossy: %v", err)
	}
	if len(res.LossyReasons) == 0 || res.LossyReasons[0].FieldPath != "made_up_field" {
		t.Errorf("res.LossyReasons: got %+v, want one entry for made_up_field", res.LossyReasons)
	}
}

func TestInstall_NativeExtensionOnClaudeCode_NotLossy_BytesPreserved(t *testing.T) {
	// ADR-0016 §8 + schema/skill.yaml: `allowed-tools` is one alias of
	// canonical_concept `claude_skill_runtime_overrides` with host
	// claude-code present in aliases[]. Installing on claude-code must
	// NOT trigger LossyError, and the bytes must round-trip identically
	// to disk (ADR-0008 byte-pass-through holds when the adapter doesn't
	// need to drop anything). This is the load-bearing slice-2 test —
	// real-world Claude skills carrying these fields install cleanly.
	src := []byte("---\n" +
		"name: native-ext-test\n" +
		"description: A skill with claude-only frontmatter; tests schema-driven lossy.\n" +
		"allowed-tools:\n" +
		"  - Read\n" +
		"---\n" +
		"Body.\n")
	skill, err := resource.ParseSkill(src)
	if err != nil {
		t.Fatalf("ParseSkill: %v", err)
	}

	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	res, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err != nil {
		t.Fatalf("Install: %v (expected no lossy — schema declares claude-code supports allowed-tools)", err)
	}
	if len(res.LossyReasons) != 0 {
		t.Errorf("res.LossyReasons: got %+v, want empty (claude-code natively supports allowed-tools)", res.LossyReasons)
	}

	got, err := os.ReadFile(res.Plan.Files[0].Path)
	if err != nil {
		t.Fatalf("ReadFile installed: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Errorf("installed bytes diverge from source (ADR-0008 violation).\n"+
			"--- source ---\n%s\n--- got ---\n%s", string(src), string(got))
	}
}
