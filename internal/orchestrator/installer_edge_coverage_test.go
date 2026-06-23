package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
	"github.com/ellarock-software/dotpack/internal/resource"
)

type unnamedCoverageResource struct{ kind resource.Kind }

func (r unnamedCoverageResource) Kind() resource.Kind        { return r.kind }
func (r unnamedCoverageResource) Extensions() map[string]any { return nil }

type namedCoverageResource struct {
	kind resource.Kind
	ext  map[string]any
}

func (r namedCoverageResource) Kind() resource.Kind        { return r.kind }
func (r namedCoverageResource) Extensions() map[string]any { return r.ext }
func (r namedCoverageResource) ResourceName() string       { return "named" }

func TestInstallerAdditionalErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "out.txt")
	plan := adapter.InstallPlan{Files: []adapter.FileWrite{{Path: target, Content: []byte("x")}}}

	badSchema := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: plan}, manifest.NewStore(filepath.Join(tmp, "schema.yaml")))
	if _, err := badSchema.Install(namedCoverageResource{kind: resource.Kind("unknown"), ext: map[string]any{"x": "y"}}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "lossy check") {
		t.Fatalf("lossy schema err=%v; want lossy check", err)
	}

	noName := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: plan}, manifest.NewStore(filepath.Join(tmp, "noname.yaml")))
	if _, err := noName.Install(unnamedCoverageResource{kind: resource.KindSkill}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "no ID-derivation") {
		t.Fatalf("resourceName err=%v; want no ID-derivation", err)
	}

	badManifest := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badManifest, []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	badCollision := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: plan}, manifest.NewStore(badManifest))
	if _, err := badCollision.Install(&resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "collision check") {
		t.Fatalf("collision load err=%v; want collision check", err)
	}

	badMKPreflight := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{
		MergedKeys: []adapter.MergedKeyWrite{{File: filepath.Join(tmp, "bad.ext"), Path: "$.x", Value: "y"}},
	}}, manifest.NewStore(filepath.Join(tmp, "mk-preflight.yaml")))
	if _, err := badMKPreflight.Install(&resource.Skill{Name: "mk", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "merged-key collision check") {
		t.Fatalf("merged-key preflight err=%v; want merged-key collision check", err)
	}

	staleDir := filepath.Join(tmp, "stale-dir")
	writeCoverageFile(t, filepath.Join(staleDir, "child"), "x")
	staleRemoval := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{
		RemoveFiles: []adapter.FileRemove{{Path: staleDir}},
	}}, manifest.NewStore(filepath.Join(tmp, "stale.yaml")))
	if _, err := staleRemoval.Install(&resource.Skill{Name: "stale", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "remove stale file") {
		t.Fatalf("remove stale err=%v; want remove stale file", err)
	}

	applyMK := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{
		MergedKeys: []adapter.MergedKeyWrite{{File: filepath.Join(tmp, "apply.ext"), Path: "$.x", Value: "y"}},
	}}, manifest.NewStore(filepath.Join(tmp, "apply.yaml")))
	if _, err := applyMK.Install(&resource.Skill{Name: "apply", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "apply merged key") {
		t.Fatalf("apply merged key err=%v; want apply merged key", err)
	}
}

func TestInstallerReinstallCleanupAndManifestSaveErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	rec := manifest.Record{
		ID:    "host:skill:cleanup",
		Scope: string(adapter.ScopeUser),
		MergedKeys: []manifest.MergedKey{{
			File:     filepath.Join(tmp, "bad.ext"),
			Path:     "$.hooks.PreToolUse",
			Op:       string(adapter.MergedKeyAppend),
			Selector: "sha256:nope",
		}},
	}
	if err := store.Upsert(rec); err != nil {
		t.Fatalf("Upsert cleanup rec: %v", err)
	}
	inst := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{}}, store)
	if _, err := inst.Install(&resource.Skill{Name: "cleanup", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "re-install cleanup") {
		t.Fatalf("cleanup err=%v; want re-install cleanup", err)
	}

	locked := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	saveStore := manifest.NewStore(filepath.Join(locked, "installs.yaml"))
	saveInst := NewInstaller(dirs.Dirs{}, coverageAdapter{host: "host", plan: adapter.InstallPlan{}}, saveStore)
	_, err := saveInst.Install(&resource.Skill{Name: "save", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true})
	if err == nil {
		t.Skip("filesystem allowed manifest save in non-writable directory")
	}
	if !strings.Contains(err.Error(), "upsert manifest") && !strings.Contains(err.Error(), "re-install cleanup") {
		t.Fatalf("manifest save err=%v; want upsert manifest/re-install cleanup", err)
	}
}

func TestWriteAtomicCreateTempErrorBranch(t *testing.T) {
	tmp := t.TempDir()
	locked := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	err := writeAtomic(adapter.FileWrite{Path: filepath.Join(locked, "out.txt"), Content: []byte("x")})
	if err == nil {
		t.Skip("filesystem allowed writeAtomic in non-writable directory")
	}
}
