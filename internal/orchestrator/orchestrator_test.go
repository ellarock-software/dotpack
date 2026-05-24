package orchestrator_test

import (
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

func TestInstall_LossyPlanRefusedWithoutAllowLossy(t *testing.T) {
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{
		Name:        "y",
		Description: "d",
		Body:        "b",
		Extensions: map[string]any{
			// allowed-tools is a Claude-only field; adapter emits
			// universal core only in slice 1 → marks plan lossy.
			"allowed-tools": []any{"Bash"},
		},
	}
	_, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err == nil {
		t.Fatal("expected lossy refusal, got nil")
	}
	var le *orchestrator.LossyError
	if !errors.As(err, &le) {
		t.Errorf("error: got %T (%v), want *orchestrator.LossyError", err, err)
	}
}

func TestInstall_LossyPlanProceedsWithAllowLossy(t *testing.T) {
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{
		Name: "z", Description: "d", Body: "b",
		Extensions: map[string]any{"allowed-tools": []any{"Bash"}},
	}
	res, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f", AllowLossy: true})
	if err != nil {
		t.Fatalf("Install with AllowLossy: %v", err)
	}
	if !res.Plan.Lossy {
		t.Error("res.Plan.Lossy: got false, want true (Extensions present)")
	}
}
