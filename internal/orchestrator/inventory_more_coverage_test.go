package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

func TestInventoryMatchingPathAndNameHelpers(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	canonical := filepath.Join(tmp, "canonical", ".agents")
	insideTarget := filepath.Join(target, ".claude", "skills", "s", "SKILL.md")
	insideCanonical := filepath.Join(canonical, "skills", "s", "SKILL.md")
	writeCoverageFile(t, insideTarget, "target")
	writeCoverageFile(t, insideCanonical, "canonical")

	if !recordMatchesTarget(manifest.Record{Files: []string{insideTarget}}, target) {
		t.Fatal("recordMatchesTarget should match file under target")
	}
	if !recordMatchesTarget(manifest.Record{MergedKeys: []manifest.MergedKey{{File: filepath.Join(target, ".mcp.json")}}}, target) {
		t.Fatal("recordMatchesTarget should match merged key under target")
	}
	if recordMatchesTarget(manifest.Record{Files: []string{filepath.Join(tmp, "other", "x")}}, target) {
		t.Fatal("recordMatchesTarget should not match outside file")
	}
	if !recordMatchesCanonical(manifest.Record{CanonicalRoot: canonical}, canonical) {
		t.Fatal("recordMatchesCanonical should match explicit canonical root")
	}
	if !recordMatchesCanonical(manifest.Record{SourcePath: insideCanonical}, canonical) {
		t.Fatal("recordMatchesCanonical should match source path under canonical root")
	}
	if !recordMatchesCanonical(manifest.Record{Source: "file://" + insideCanonical}, canonical) {
		t.Fatal("recordMatchesCanonical should match file:// source under canonical root")
	}
	if recordMatchesCanonical(manifest.Record{SourcePath: filepath.Join(tmp, "elsewhere", "SKILL.md")}, canonical) {
		t.Fatal("recordMatchesCanonical should not match outside source")
	}
	if !pathWithin(target, target) || !pathWithin(target, insideTarget) || pathWithin(target, filepath.Join(tmp, "target-sibling")) || pathWithin("", insideTarget) {
		t.Fatal("pathWithin returned unexpected values")
	}
	if shortName("plain") != "plain" || shortName("host:kind:name") != "name" {
		t.Fatal("shortName returned unexpected values")
	}
}

func TestRemoveEmptyDirsAndUninstallRecordErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	if err := removeEmptyDirsUnder(filepath.Join(tmp, "missing")); err == nil {
		t.Fatal("removeEmptyDirsUnder missing root should error")
	}
	nonEmpty := filepath.Join(tmp, "nonempty")
	writeCoverageFile(t, filepath.Join(nonEmpty, "child.txt"), "x")
	if err := removeEmptyDirsUnder(nonEmpty); err == nil || !os.IsExist(err) {
		t.Fatalf("removeEmptyDirsUnder non-empty err=%v; want exist", err)
	}
	empty := filepath.Join(tmp, "empty", "nested")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	if err := removeEmptyDirsUnder(filepath.Join(tmp, "empty")); err != nil {
		t.Fatalf("removeEmptyDirsUnder empty: %v", err)
	}

	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	reader := NewReader(dirs.Dirs{}, store)
	dirFile := filepath.Join(tmp, "owned-dir")
	writeCoverageFile(t, filepath.Join(dirFile, "child"), "x")
	if _, err := reader.uninstallRecord(manifest.Record{ID: "x", Files: []string{dirFile}}); err == nil || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("uninstallRecord non-empty dir err=%v; want remove", err)
	}
	if _, err := reader.uninstallRecord(manifest.Record{ID: "x", MergedKeys: []manifest.MergedKey{{File: filepath.Join(tmp, "config.unsupported"), Path: "$.x"}}}); err == nil || !strings.Contains(err.Error(), "un-merge key") {
		t.Fatalf("uninstallRecord bad merged key err=%v; want un-merge key", err)
	}
}

func TestReaderUninstallListInventoryAndResetErrorBranches(t *testing.T) {
	tmp := t.TempDir()
	badManifest := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badManifest, []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	badReader := NewReader(dirs.Dirs{}, manifest.NewStore(badManifest))
	if _, err := badReader.List(); err == nil {
		t.Fatal("List should return manifest load error")
	}
	if _, err := badReader.Uninstall("x"); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("Uninstall load error = %v; want load manifest", err)
	}
	if _, err := badReader.InventoryFiles(nil, nil, ""); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("InventoryFiles load error = %v; want load manifest", err)
	}
	if _, err := badReader.ResetMaterialized(ResetOptions{}); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("ResetMaterialized load error = %v; want load manifest", err)
	}

	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	ambiguousA := manifest.Record{ID: "host:skill:dup", Scope: string(adapter.ScopeProject), TargetRoot: filepath.Join(tmp, "a")}
	ambiguousB := manifest.Record{ID: "host:skill:dup", Scope: string(adapter.ScopeProject), TargetRoot: filepath.Join(tmp, "b")}
	hint := manifest.Record{ID: "host:agent:hint", Scope: string(adapter.ScopeProject), TargetRoot: filepath.Join(tmp, "c")}
	for _, rec := range []manifest.Record{ambiguousA, ambiguousB, hint} {
		if err := store.Upsert(rec); err != nil {
			t.Fatalf("Upsert(%s): %v", rec.ID, err)
		}
	}
	reader := NewReader(dirs.Dirs{}, store)
	if _, err := reader.Uninstall("host:skill:dup"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Uninstall ambiguous err=%v; want ambiguous", err)
	}
	if _, err := reader.Uninstall("missing:skill:hint"); err == nil || !strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("Uninstall hint err=%v; want did you mean", err)
	}
	if _, err := reader.Uninstall("missing:skill:nope"); err == nil || !strings.Contains(err.Error(), "no install") {
		t.Fatalf("Uninstall missing err=%v; want no install", err)
	}

	dirObservation := filepath.Join(tmp, "observed-dir")
	if err := os.MkdirAll(dirObservation, 0o755); err != nil {
		t.Fatalf("mkdir observed dir: %v", err)
	}
	if _, err := reader.InventoryFiles([]FileObservation{{Path: dirObservation}}, nil, ""); err == nil || !strings.Contains(err.Error(), "hash untracked file") {
		t.Fatalf("InventoryFiles dir observation err=%v; want hash untracked file", err)
	}

	claimedDir := filepath.Join(tmp, "claimed-dir")
	if err := os.MkdirAll(claimedDir, 0o755); err != nil {
		t.Fatalf("mkdir claimed dir: %v", err)
	}
	if err := store.Upsert(manifest.Record{
		ID:         "host:skill:claimed-dir",
		Files:      []string{claimedDir},
		FileClaims: []manifest.FileClaim{{Path: claimedDir, SHA256: "sha256:expected"}},
	}); err != nil {
		t.Fatalf("Upsert claimed dir: %v", err)
	}
	if _, err := reader.InventoryFiles([]FileObservation{{Path: claimedDir}}, nil, ""); err == nil || !strings.Contains(err.Error(), "hash observed file") {
		t.Fatalf("InventoryFiles claimed dir err=%v; want hash observed file", err)
	}

	sortA := filepath.Join(tmp, "sort-a.md")
	sortB := filepath.Join(tmp, "sort-b.md")
	sortAHash := writeCoverageFile(t, sortA, "a")
	sortBHash := writeCoverageFile(t, sortB, "b")
	items, err := NewReader(dirs.Dirs{}, manifest.NewStore(filepath.Join(tmp, "sort.yaml"))).InventoryFiles(
		[]FileObservation{{Path: ""}, {Path: sortB}, {Path: sortA}},
		[]ExpectedFile{{Path: ""}, {Path: sortB, SHA256: sortBHash}, {Path: sortA, SHA256: sortAHash}},
		"",
	)
	if err != nil {
		t.Fatalf("InventoryFiles sort/empty entries: %v", err)
	}
	if len(items) != 2 || items[0].Path != sortA || items[1].Path != sortB {
		t.Fatalf("InventoryFiles sorted items = %#v; want sort-a then sort-b", items)
	}

	untrackedDir := filepath.Join(tmp, "untracked-dir")
	writeCoverageFile(t, filepath.Join(untrackedDir, "child"), "x")
	if _, err := reader.ResetMaterialized(ResetOptions{IncludeUntracked: true, ObservedFiles: []FileObservation{{Path: untrackedDir}}}); err == nil || !strings.Contains(err.Error(), "remove untracked") {
		t.Fatalf("ResetMaterialized remove dir err=%v; want remove untracked", err)
	}

	resetStore := manifest.NewStore(filepath.Join(tmp, "reset.yaml"))
	ownedDir := filepath.Join(tmp, "owned-dir-reset")
	writeCoverageFile(t, filepath.Join(ownedDir, "child"), "x")
	if err := resetStore.Upsert(manifest.Record{ID: "host:skill:owned-dir", Files: []string{ownedDir}}); err != nil {
		t.Fatalf("Upsert reset dir: %v", err)
	}
	if _, err := NewReader(dirs.Dirs{}, resetStore).ResetMaterialized(ResetOptions{}); err == nil || !strings.Contains(err.Error(), "remove") {
		t.Fatalf("ResetMaterialized owned dir err=%v; want remove", err)
	}
}
