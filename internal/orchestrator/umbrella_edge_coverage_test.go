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

func TestUmbrellaInstallAdditionalErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	writer := umbrellaFakeAdapter{host: "writer", plan: adapter.InstallPlan{}}

	badSchema := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.Kind("unknown"): {writer},
	}, manifest.NewStore(filepath.Join(tmp, "schema.yaml")))
	if _, err := badSchema.Install(namedCoverageResource{kind: resource.Kind("unknown"), ext: map[string]any{"x": "y"}}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "lossy check") {
		t.Fatalf("umbrella lossy schema err=%v; want lossy check", err)
	}

	noName := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {writer},
	}, manifest.NewStore(filepath.Join(tmp, "noname.yaml")))
	if _, err := noName.Install(unnamedCoverageResource{kind: resource.KindSkill}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "no ID-derivation") {
		t.Fatalf("umbrella resourceName err=%v; want no ID-derivation", err)
	}

	badManifest := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badManifest, []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	badCollision := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {writer},
	}, manifest.NewStore(badManifest))
	if _, err := badCollision.Install(&resource.Skill{Name: "s", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "collision check") {
		t.Fatalf("umbrella collision load err=%v; want collision check", err)
	}

	badMKPreflight := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {umbrellaFakeAdapter{host: "writer", plan: adapter.InstallPlan{MergedKeys: []adapter.MergedKeyWrite{{File: filepath.Join(tmp, "bad.ext"), Path: "$.x", Value: "y"}}}}},
	}, manifest.NewStore(filepath.Join(tmp, "mk-preflight.yaml")))
	if _, err := badMKPreflight.Install(&resource.Skill{Name: "mk", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "merged-key collision check") {
		t.Fatalf("umbrella merged-key preflight err=%v; want merged-key collision check", err)
	}

	staleDir := filepath.Join(tmp, "stale-dir")
	writeCoverageFile(t, filepath.Join(staleDir, "child"), "x")
	staleRemoval := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {umbrellaFakeAdapter{host: "writer", plan: adapter.InstallPlan{RemoveFiles: []adapter.FileRemove{{Path: staleDir}}}}},
	}, manifest.NewStore(filepath.Join(tmp, "stale.yaml")))
	if _, err := staleRemoval.Install(&resource.Skill{Name: "stale", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "remove stale file") {
		t.Fatalf("umbrella remove stale err=%v; want remove stale file", err)
	}

	applyFileParent := filepath.Join(tmp, "file-parent")
	if err := os.WriteFile(applyFileParent, []byte("x"), 0o644); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	applyFile := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {umbrellaFakeAdapter{host: "writer", plan: adapter.InstallPlan{Files: []adapter.FileWrite{{Path: filepath.Join(applyFileParent, "child"), Content: []byte("x")}}}}},
	}, manifest.NewStore(filepath.Join(tmp, "apply-file.yaml")))
	if _, err := applyFile.Install(&resource.Skill{Name: "file", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "apply file") {
		t.Fatalf("umbrella apply file err=%v; want apply file", err)
	}

	applyMK := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {umbrellaFakeAdapter{host: "writer", plan: adapter.InstallPlan{MergedKeys: []adapter.MergedKeyWrite{{File: filepath.Join(tmp, "apply.ext"), Path: "$.x", Value: "y"}}}}},
	}, manifest.NewStore(filepath.Join(tmp, "apply-mk.yaml")))
	if _, err := applyMK.Install(&resource.Skill{Name: "apply", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "apply merged key") {
		t.Fatalf("umbrella apply merged key err=%v; want apply merged key", err)
	}
}

func TestUmbrellaReinstallCleanupAndManifestSaveErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	writer := umbrellaFakeAdapter{host: "writer", plan: adapter.InstallPlan{}}
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	rec := manifest.Record{
		ID:    "all:skill:cleanup",
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
	cleanup := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {writer},
	}, store)
	if _, err := cleanup.Install(&resource.Skill{Name: "cleanup", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true}); err == nil || !strings.Contains(err.Error(), "re-install cleanup") {
		t.Fatalf("umbrella cleanup err=%v; want re-install cleanup", err)
	}

	locked := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(locked, 0o500); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	saveStore := manifest.NewStore(filepath.Join(locked, "installs.yaml"))
	save := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{writer}, map[resource.Kind][]adapter.Adapter{
		resource.KindSkill: {writer},
	}, saveStore)
	_, err := save.Install(&resource.Skill{Name: "save", Description: "d", Body: "b"}, adapter.ScopeUser, InstallOptions{Force: true})
	if err == nil {
		t.Skip("filesystem allowed umbrella manifest save in non-writable directory")
	}
	if !strings.Contains(err.Error(), "upsert manifest") && !strings.Contains(err.Error(), "re-install cleanup") {
		t.Fatalf("umbrella save err=%v; want upsert manifest/re-install cleanup", err)
	}
}

func TestUmbrellaAggregateLossySchemaError(t *testing.T) {
	u := NewUmbrellaInstaller(dirs.Dirs{}, "all", []adapter.Adapter{umbrellaFakeAdapter{host: "writer"}}, nil, manifest.NewStore(filepath.Join(t.TempDir(), "installs.yaml")))
	if _, err := u.aggregateLossy(namedCoverageResource{kind: resource.Kind("unknown"), ext: map[string]any{"x": "y"}}); err == nil || !strings.Contains(err.Error(), "sub-adapter") {
		t.Fatalf("aggregateLossy schema err=%v; want sub-adapter", err)
	}
}
