package orchestrator_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	skill := (&resource.Skill{Name: "y", Description: "d", Body: "b"}).
		WithExtensions(map[string]any{"made_up_field": "foo"})
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

	skill := (&resource.Skill{Name: "z", Description: "d", Body: "b"}).
		WithExtensions(map[string]any{"made_up_field": "foo"})
	res, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f", AllowLossy: true})
	if err != nil {
		t.Fatalf("Install with AllowLossy: %v", err)
	}
	if len(res.LossyReasons) == 0 || res.LossyReasons[0].FieldPath != "made_up_field" {
		t.Errorf("res.LossyReasons: got %+v, want one entry for made_up_field", res.LossyReasons)
	}
}

func TestLossyError_Error_RendersConceptAndSupportedHosts(t *testing.T) {
	// Hostile-review #3: LossyError used to print only field names,
	// throwing away CanonicalConcept + SupportedHosts that §8 had
	// collected. The new formatter surfaces both so users know WHY
	// each field was rejected and WHERE it would have worked.
	le := &orchestrator.LossyError{
		Host: "gemini",
		Reasons: []adapter.LossyReason{
			{FieldPath: "allowed-tools", CanonicalConcept: "claude_skill_runtime_overrides", SupportedHosts: []string{"claude-code"}},
			{FieldPath: "my_typo", CanonicalConcept: "", SupportedHosts: nil},
		},
	}
	msg := le.Error()
	for _, want := range []string{
		"gemini",
		"allowed-tools",
		"claude_skill_runtime_overrides",
		"claude-code",
		"my_typo",
		"unknown field",
		"--allow-lossy",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("LossyError.Error() missing %q. Full message:\n%s", want, msg)
		}
	}
}

func TestInstall_ProjectScope_ManifestRecordHasAbsolutePath(t *testing.T) {
	// Slice 2 task #2: project-scope installs used to compute paths
	// CWD-relative (`./.claude/skills/<name>/SKILL.md`), which meant
	// the manifest record's Files / TargetDir were also CWD-relative —
	// uninstall from a different CWD would not resolve them, and #5
	// (uninstall + list) is unbuildable on that footing. dirs.Dirs
	// gained ProjectHome, resolved at FromEnv time from
	// DOTPACK_PROJECT_HOME or CWD; the adapter must root project-scope
	// paths under it. This test pins the load-bearing claim: after
	// install, every path in the manifest record is absolute.
	projectHome := t.TempDir()
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir(), ProjectHome: projectHome}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{Name: "proj-skill", Description: "d", Body: "b\n"}
	res, err := orch.Install(skill, adapter.ScopeProject, orchestrator.InstallOptions{Source: "f"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if !filepath.IsAbs(res.Record.TargetDir) {
		t.Errorf("manifest Record.TargetDir must be absolute; got %q", res.Record.TargetDir)
	}
	for _, f := range res.Record.Files {
		if !filepath.IsAbs(f) {
			t.Errorf("manifest Record.Files entry must be absolute; got %q", f)
		}
	}
	wantPrefix := filepath.Join(projectHome, ".claude", "skills", "proj-skill")
	if !strings.HasPrefix(res.Record.TargetDir, wantPrefix) {
		t.Errorf("Record.TargetDir %q should be under ProjectHome/.claude/skills/proj-skill (%q)", res.Record.TargetDir, wantPrefix)
	}
}

func TestInstall_CollisionWithUntrackedFile_ReturnsCollisionError(t *testing.T) {
	// Slice 2 task #3: writeAtomic used to rename over any existing
	// target file unconditionally, clobbering user-edited skills (or
	// orphans left by a partial prior install). The orchestrator now
	// pre-flights: if the manifest has no record for this install's ID
	// AND any path in plan.Files exists on disk, refuse with a
	// CollisionError naming the offending paths. --force bypasses
	// (see TestInstall_CollisionBypassedWithForce).
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	// Plant an untracked file at the target path (simulates user edit
	// OR an orphan from a partial prior install).
	targetDir := filepath.Join(d.ClaudeHome, "skills", "collide")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("setup MkdirAll: %v", err)
	}
	targetPath := filepath.Join(targetDir, "SKILL.md")
	if err := os.WriteFile(targetPath, []byte("USER EDIT"), 0o644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	skill := &resource.Skill{Name: "collide", Description: "d", Body: "b"}
	_, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err == nil {
		t.Fatal("expected CollisionError, got nil")
	}
	var ce *orchestrator.CollisionError
	if !errors.As(err, &ce) {
		t.Fatalf("error: got %T (%v), want *orchestrator.CollisionError", err, err)
	}
	if len(ce.Paths) != 1 || ce.Paths[0] != targetPath {
		t.Errorf("CollisionError.Paths: got %v, want [%q]", ce.Paths, targetPath)
	}

	// Untracked file is preserved, not clobbered.
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile after refused install: %v", err)
	}
	if string(got) != "USER EDIT" {
		t.Errorf("untracked file mutated: got %q, want %q", string(got), "USER EDIT")
	}
}

func TestInstall_CollisionBypassedWithForce(t *testing.T) {
	// --force is the escape hatch: the user has audited the collision
	// and wants dotpack to overwrite. Mirror --allow-lossy's contract.
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	targetDir := filepath.Join(d.ClaudeHome, "skills", "force-me")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("setup MkdirAll: %v", err)
	}
	targetPath := filepath.Join(targetDir, "SKILL.md")
	if err := os.WriteFile(targetPath, []byte("OLD"), 0o644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	skill := &resource.Skill{Name: "force-me", Description: "d", Body: "new body\n"}
	res, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f", Force: true})
	if err != nil {
		t.Fatalf("Install with Force: %v", err)
	}
	got, err := os.ReadFile(res.Plan.Files[0].Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) == "OLD" {
		t.Errorf("file was not overwritten under --force; still contains %q", "OLD")
	}
}

func TestInstall_ReinstallOverTrackedFile_NoCollision(t *testing.T) {
	// Re-install: same ID is present in the manifest from a prior
	// install. The collision check must NOT fire (this is the normal
	// "update an installed resource" path, not a collision). #4
	// guarantees the manifest record gets replaced in place, not
	// duplicated.
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	skill := &resource.Skill{Name: "reinstall", Description: "d", Body: "v1\n"}
	if _, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"}); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	skill2 := &resource.Skill{Name: "reinstall", Description: "d", Body: "v2\n"}
	if _, err := orch.Install(skill2, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"}); err != nil {
		t.Fatalf("second Install (re-install) must not collide; got %v", err)
	}

	loaded, err := mf.Load()
	if err != nil {
		t.Fatalf("Load manifest: %v", err)
	}
	if len(loaded.Installs) != 1 {
		t.Errorf("manifest len after re-install: got %d, want 1 (dedupe per #4)", len(loaded.Installs))
	}
}

func TestInstall_CollisionDetectsSymlinkAtTarget(t *testing.T) {
	// Hostile-review #2: os.Stat follows symlinks, so a symlink at the
	// install target either (a) hides the symlink-ness from the user
	// and silently gets replaced by --force, or (b) escapes detection
	// when the symlink target is missing (dangling). The fix is
	// os.Lstat — a symlink IS a collision regardless of target state.
	// This test pins both branches: dangling symlink (would have been
	// invisible under os.Stat) AND a symlink to an existing file
	// (would have been seen but its symlink-ness lost on --force).
	d := dirs.Dirs{ClaudeHome: t.TempDir(), DotpackHome: t.TempDir()}
	a := claudecode.New(d)
	mf := manifest.NewStore(filepath.Join(d.DotpackHome, "installs.yaml"))
	orch := orchestrator.New(d, a, mf)

	targetDir := filepath.Join(d.ClaudeHome, "skills", "symlinked")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("setup MkdirAll: %v", err)
	}
	targetPath := filepath.Join(targetDir, "SKILL.md")
	// Symlink pointing at a path that does not exist (dangling).
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), targetPath); err != nil {
		t.Fatalf("setup Symlink: %v", err)
	}

	skill := &resource.Skill{Name: "symlinked", Description: "d", Body: "b"}
	_, err := orch.Install(skill, adapter.ScopeUser, orchestrator.InstallOptions{Source: "f"})
	if err == nil {
		t.Fatal("expected CollisionError on symlinked target (even when dangling), got nil")
	}
	var ce *orchestrator.CollisionError
	if !errors.As(err, &ce) {
		t.Fatalf("error: got %T (%v), want *orchestrator.CollisionError", err, err)
	}
	if len(ce.Paths) != 1 || ce.Paths[0] != targetPath {
		t.Errorf("CollisionError.Paths: got %v, want [%q]", ce.Paths, targetPath)
	}

	// Symlink is preserved, not replaced.
	info, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("Lstat after refused install: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("target should still be a symlink after refusal; got mode %v", info.Mode())
	}
}

func TestCollisionError_Error_RendersPathsAndForceHint(t *testing.T) {
	// CollisionError mirrors LossyError's contract: surface the
	// offending paths AND the bypass flag in one message so the user
	// can act without re-running for more diagnostics.
	ce := &orchestrator.CollisionError{
		Paths: []string{"/home/x/.claude/skills/a/SKILL.md", "/home/x/.claude/skills/b/SKILL.md"},
	}
	msg := ce.Error()
	for _, want := range []string{
		"collide",
		"/home/x/.claude/skills/a/SKILL.md",
		"/home/x/.claude/skills/b/SKILL.md",
		"--force",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("CollisionError.Error() missing %q. Full message:\n%s", want, msg)
		}
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
