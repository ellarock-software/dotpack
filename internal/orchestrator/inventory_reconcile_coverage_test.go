package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/manifest"
)

func writeCoverageFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return hashBytes([]byte(body))
}

func TestInventoryFilesClassifiesTrackedDriftMissingAndUntracked(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	canonical := filepath.Join(tmp, "canonical")
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))

	trackedPath := filepath.Join(target, "tracked.md")
	trackedHash := writeCoverageFile(t, trackedPath, "tracked")
	driftedPath := filepath.Join(target, "drifted.md")
	driftedExpected := hashBytes([]byte("expected"))
	writeCoverageFile(t, driftedPath, "actual")
	missingPath := filepath.Join(target, "missing.md")
	canonicalPath := filepath.Join(target, "canonical.md")
	canonicalHash := writeCoverageFile(t, canonicalPath, "canonical")
	foreignPath := filepath.Join(target, "foreign.md")
	writeCoverageFile(t, foreignPath, "foreign")
	outsidePath := filepath.Join(tmp, "outside.md")
	outsideHash := writeCoverageFile(t, outsidePath, "outside")

	for _, rec := range []manifest.Record{
		{
			ID:         "claude-code:skill:tracked",
			Agent:      "claude-code",
			Kind:       "skill",
			TargetRoot: target,
			Files:      []string{trackedPath, driftedPath, missingPath},
			FileClaims: []manifest.FileClaim{
				{Path: trackedPath, SHA256: trackedHash},
				{Path: driftedPath, SHA256: driftedExpected},
				{Path: missingPath, SHA256: hashBytes([]byte("missing"))},
			},
		},
		{ID: "claude-code:skill:outside", Agent: "claude-code", Kind: "skill", TargetRoot: filepath.Join(tmp, "other"), Files: []string{outsidePath}, FileClaims: []manifest.FileClaim{{Path: outsidePath, SHA256: outsideHash}}},
	} {
		if err := store.Upsert(rec); err != nil {
			t.Fatalf("Upsert(%s): %v", rec.ID, err)
		}
	}

	items, err := NewReader(dirs.Dirs{}, store).InventoryFiles(
		[]FileObservation{
			{Path: trackedPath, RelPath: "tracked.md", TargetDir: target, Agent: "claude-code", Kind: "skill", Name: "tracked"},
			{Path: driftedPath, RelPath: "drifted.md", TargetDir: target, Agent: "claude-code", Kind: "skill", Name: "drifted"},
			{Path: canonicalPath, RelPath: "canonical.md", TargetDir: target, Agent: "claude-code", Kind: "skill", Name: "canonical"},
			{Path: foreignPath, RelPath: "foreign.md", TargetDir: target, Agent: "claude-code", Kind: "skill", Name: "foreign"},
		},
		[]ExpectedFile{{Path: canonicalPath, Source: filepath.Join(canonical, "canonical.md"), SHA256: canonicalHash}},
		target,
	)
	if err != nil {
		t.Fatalf("InventoryFiles: %v", err)
	}
	statusByPath := map[string]string{}
	for _, item := range items {
		statusByPath[item.Path] = item.Status
	}
	want := map[string]string{
		trackedPath:   InventoryTracked,
		driftedPath:   InventoryDrifted,
		missingPath:   InventoryMissing,
		canonicalPath: InventoryCanonicalUntracked,
		foreignPath:   InventoryForeignUntracked,
	}
	if len(statusByPath) != len(want) {
		t.Fatalf("inventory items = %#v; want statuses for %#v", items, want)
	}
	for path, status := range want {
		if statusByPath[path] != status {
			t.Fatalf("status[%s]=%q; want %q (items=%#v)", path, statusByPath[path], status, items)
		}
	}
}

func TestResetMaterializedRemovesMatchingRecordsAndOptionalUntracked(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "target")
	canonical := filepath.Join(tmp, "canonical")
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	owned := filepath.Join(target, "skill", "SKILL.md")
	writeCoverageFile(t, owned, "owned")
	untracked := filepath.Join(target, "orphan.md")
	writeCoverageFile(t, untracked, "orphan")
	missingUntracked := filepath.Join(target, "missing-orphan.md")
	other := filepath.Join(tmp, "other", "keep.md")
	writeCoverageFile(t, other, "keep")

	matching := manifest.Record{
		ID:            "claude-code:skill:owned",
		Scope:         string(adapter.ScopeProject),
		TargetRoot:    target,
		CanonicalRoot: canonical,
		TargetDir:     filepath.Dir(owned),
		Files:         []string{owned},
	}
	nonMatching := manifest.Record{ID: "claude-code:skill:keep", TargetRoot: filepath.Dir(other), Files: []string{other}}
	if err := store.Upsert(matching); err != nil {
		t.Fatalf("Upsert matching: %v", err)
	}
	if err := store.Upsert(nonMatching); err != nil {
		t.Fatalf("Upsert nonMatching: %v", err)
	}

	result, err := NewReader(dirs.Dirs{}, store).ResetMaterialized(ResetOptions{
		TargetRoot:       target,
		CanonicalRoot:    canonical,
		IncludeUntracked: true,
		ObservedFiles: []FileObservation{
			{Path: owned, TargetDir: filepath.Dir(owned)},
			{Path: untracked, TargetDir: target},
			{Path: missingUntracked, TargetDir: target},
		},
	})
	if err != nil {
		t.Fatalf("ResetMaterialized: %v", err)
	}
	if len(result.Uninstalled) != 1 || result.Uninstalled[0].Record.ID != matching.ID {
		t.Fatalf("Uninstalled = %#v; want matching record", result.Uninstalled)
	}
	if len(result.RemovedUntracked) != 1 || result.RemovedUntracked[0] != untracked {
		t.Fatalf("RemovedUntracked = %#v; want %s", result.RemovedUntracked, untracked)
	}
	if len(result.MissingUntracked) != 1 || result.MissingUntracked[0] != missingUntracked {
		t.Fatalf("MissingUntracked = %#v; want %s", result.MissingUntracked, missingUntracked)
	}
	if _, err := os.Stat(owned); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned file should be removed; stat err=%v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("non-matching file should remain: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Installs) != 1 || loaded.Installs[0].ID != nonMatching.ID {
		t.Fatalf("manifest after reset = %#v; want only non-matching record", loaded.Installs)
	}
}

func TestReconcileAndPruneStaleRecords(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "project")
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	present := filepath.Join(project, "present.md")
	presentHash := writeCoverageFile(t, present, "present")
	drifted := filepath.Join(project, "drifted.md")
	writeCoverageFile(t, drifted, "changed")
	missing := filepath.Join(project, "missing.md")
	config := filepath.Join(project, ".mcp.json")
	if err := applyMergedKey(adapter.MergedKeyWrite{File: config, Path: "$.mcpServers.github", Value: map[string]any{"command": "npx"}}); err != nil {
		t.Fatalf("applyMergedKey: %v", err)
	}
	staleDir := filepath.Join(project, "stale")

	records := []manifest.Record{
		{ID: "claude-code:skill:present", Scope: string(adapter.ScopeProject), TargetRoot: project, Files: []string{present}, FileClaims: []manifest.FileClaim{{Path: present, SHA256: presentHash}}},
		{ID: "claude-code:skill:drift", Scope: string(adapter.ScopeProject), TargetRoot: project, Files: []string{drifted}, FileClaims: []manifest.FileClaim{{Path: drifted, SHA256: hashBytes([]byte("original"))}}},
		{ID: "claude-code:skill:missing", Scope: string(adapter.ScopeProject), TargetRoot: project, TargetDir: staleDir, Files: []string{missing}},
		{ID: "claude-code:mcp-server:github", Scope: string(adapter.ScopeProject), TargetRoot: project, MergedKeys: []manifest.MergedKey{{File: config, Path: "$.mcpServers.github"}}},
		{ID: "claude-code:mcp-server:missing", Scope: string(adapter.ScopeProject), TargetRoot: project, MergedKeys: []manifest.MergedKey{{File: config, Path: "$.mcpServers.nope"}}},
		{ID: "claude-code:skill:other-project", Scope: string(adapter.ScopeProject), TargetRoot: filepath.Join(tmp, "other"), Files: []string{filepath.Join(tmp, "other", "x.md")}},
	}
	for _, rec := range records {
		if err := store.Upsert(rec); err != nil {
			t.Fatalf("Upsert(%s): %v", rec.ID, err)
		}
	}
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll staleDir: %v", err)
	}

	reader := NewReader(dirs.Dirs{ProjectHome: project}, store)
	statuses, err := reader.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	byID := map[string]ReconcileStatus{}
	for _, st := range statuses {
		byID[st.Record.ID] = st
	}
	if len(byID) != 5 {
		t.Fatalf("statuses = %#v; want project-scoped records only", statuses)
	}
	if !byID["claude-code:skill:present"].HasClaims() || byID["claude-code:skill:present"].HasDrift() {
		t.Fatalf("present status wrong: %#v", byID["claude-code:skill:present"])
	}
	if len(byID["claude-code:skill:drift"].DriftedFiles) != 1 || !byID["claude-code:skill:drift"].HasDrift() {
		t.Fatalf("drift status wrong: %#v", byID["claude-code:skill:drift"])
	}
	if len(byID["claude-code:skill:missing"].MissingFiles) != 1 {
		t.Fatalf("missing status wrong: %#v", byID["claude-code:skill:missing"])
	}
	if len(byID["claude-code:mcp-server:github"].PresentMergedKeys) != 1 {
		t.Fatalf("present merged key status wrong: %#v", byID["claude-code:mcp-server:github"])
	}
	if len(byID["claude-code:mcp-server:missing"].MissingMergedKeys) != 1 {
		t.Fatalf("missing merged key status wrong: %#v", byID["claude-code:mcp-server:missing"])
	}

	pruned, err := reader.PruneStaleRecords()
	if err != nil {
		t.Fatalf("PruneStaleRecords: %v", err)
	}
	var prunedIDs []string
	for _, rec := range pruned.Pruned {
		prunedIDs = append(prunedIDs, rec.ID)
	}
	sort.Strings(prunedIDs)
	if len(prunedIDs) != 2 || prunedIDs[0] != "claude-code:mcp-server:missing" || prunedIDs[1] != "claude-code:skill:missing" {
		t.Fatalf("Pruned IDs = %v; want missing file and missing merged-key records", prunedIDs)
	}
	if len(pruned.Partial) != 1 || pruned.Partial[0].Record.ID != "claude-code:skill:drift" {
		t.Fatalf("Partial = %#v; want drift record kept", pruned.Partial)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	remaining := map[string]bool{}
	for _, rec := range loaded.Installs {
		remaining[rec.ID] = true
	}
	if remaining["claude-code:skill:missing"] || remaining["claude-code:mcp-server:missing"] {
		t.Fatalf("stale records still present: %#v", loaded.Installs)
	}
	if !remaining["claude-code:skill:drift"] || !remaining["claude-code:skill:present"] {
		t.Fatalf("live records missing after prune: %#v", loaded.Installs)
	}
}

func TestReconcileAdditionalErrorAndMergedKeyBranches(t *testing.T) {
	tmp := t.TempDir()
	badManifest := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(badManifest, []byte("installs: ["), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	badReader := NewReader(dirs.Dirs{}, manifest.NewStore(badManifest))
	if _, err := badReader.Reconcile(); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("Reconcile bad manifest err=%v; want load manifest", err)
	}
	if _, err := badReader.PruneStaleRecords(); err == nil || !strings.Contains(err.Error(), "load manifest") {
		t.Fatalf("PruneStaleRecords bad manifest err=%v; want load manifest", err)
	}

	project := filepath.Join(tmp, "project")
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	dirClaim := filepath.Join(project, "claimed-dir")
	if err := os.MkdirAll(dirClaim, 0o755); err != nil {
		t.Fatalf("mkdir claimed dir: %v", err)
	}
	badConfig := filepath.Join(project, "unsupported.ext")
	if err := store.Upsert(manifest.Record{
		ID:         "claude-code:skill:dir-claim",
		Scope:      string(adapter.ScopeProject),
		TargetRoot: project,
		Files:      []string{dirClaim},
		FileClaims: []manifest.FileClaim{{Path: dirClaim, SHA256: "sha256:expected"}},
	}); err != nil {
		t.Fatalf("Upsert dir claim: %v", err)
	}
	if err := store.Upsert(manifest.Record{
		ID:         "claude-code:mcp-server:bad-config",
		Scope:      string(adapter.ScopeProject),
		TargetRoot: project,
		MergedKeys: []manifest.MergedKey{{File: badConfig, Path: "$.x"}},
	}); err != nil {
		t.Fatalf("Upsert bad config: %v", err)
	}

	reader := NewReader(dirs.Dirs{ProjectHome: project}, store)
	statuses, err := reader.Reconcile()
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	byID := map[string]ReconcileStatus{}
	for _, st := range statuses {
		byID[st.Record.ID] = st
	}
	if got := byID["claude-code:skill:dir-claim"].Errors; len(got) != 1 || !strings.Contains(got[0], "hash file") {
		t.Fatalf("dir claim errors = %#v; want hash file", got)
	}
	if got := byID["claude-code:mcp-server:bad-config"].Errors; len(got) != 1 || !strings.Contains(got[0], "unsupported file extension") {
		t.Fatalf("bad config errors = %#v; want unsupported extension", got)
	}

	pruned, err := reader.PruneStaleRecords()
	if err != nil {
		t.Fatalf("PruneStaleRecords: %v", err)
	}
	if len(pruned.Pruned) != 0 || len(pruned.Partial) != 2 {
		t.Fatalf("pruned = %#v; want both error records retained as partial", pruned)
	}

	missingConfig := filepath.Join(project, "missing.json")
	if exists, err := mergedKeyExists(manifest.MergedKey{File: missingConfig, Path: "$.x"}); err != nil || exists {
		t.Fatalf("missing merged file exists=%v err=%v; want false,nil", exists, err)
	}

	config := filepath.Join(project, "hooks.json")
	writeCoverageFile(t, config, `{"hooks":{"PreToolUse":{"matcher":"Bash"}}}`)
	if exists, err := mergedKeyExists(manifest.MergedKey{File: config, Path: "$.hooks.Missing", Op: string(adapter.MergedKeyAppend), Selector: "sha256:nope"}); err != nil || exists {
		t.Fatalf("missing append path exists=%v err=%v; want false,nil", exists, err)
	}
	if exists, err := mergedKeyExists(manifest.MergedKey{File: config, Path: "$.hooks.PreToolUse", Op: string(adapter.MergedKeyAppend), Selector: "sha256:nope"}); err != nil || exists {
		t.Fatalf("non-array append path exists=%v err=%v; want false,nil", exists, err)
	}
}
