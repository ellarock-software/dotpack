package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ellarock/dotpack/internal/manifest"
)

func TestStore_LoadMissingFileReturnsEmpty(t *testing.T) {
	store := manifest.NewStore(filepath.Join(t.TempDir(), "installs.yaml"))
	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load on missing file should not error; got %v", err)
	}
	if len(m.Installs) != 0 {
		t.Errorf("len(Installs): got %d, want 0", len(m.Installs))
	}
}

func TestStore_UpsertThenLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)

	rec := manifest.Record{
		ID:          "claude-code:skill:hello",
		Source:      "file:///some/path/SKILL.md",
		Kind:        "skill",
		Agent:       "claude-code",
		Scope:       "user",
		TargetDir:   "/home/x/.claude/skills/hello",
		Files:       []string{"/home/x/.claude/skills/hello/SKILL.md"},
		CacheKey:    "sha256:abc",
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := store.Upsert(rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Installs) != 1 {
		t.Fatalf("len(Installs): got %d, want 1", len(m.Installs))
	}
	got := m.Installs[0]
	if got.ID != rec.ID || got.Source != rec.Source || got.Kind != rec.Kind {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, rec)
	}
	if len(got.Files) != 1 || got.Files[0] != rec.Files[0] {
		t.Errorf("Files round-trip: got %v, want %v", got.Files, rec.Files)
	}
}

func TestStore_UpsertIsAtomic_NoLingeringTempFile(t *testing.T) {
	// Per ADR-0008: write atomically (write to tmp, rename). After
	// Upsert returns, no .tmp scratch files should remain in the
	// manifest's directory.
	tmp := t.TempDir()
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	if err := store.Upsert(manifest.Record{ID: "x", InstalledAt: "now"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("lingering temp file: %s", e.Name())
		}
	}
}

func TestStore_UpsertCreatesParentDir(t *testing.T) {
	// installs.yaml lives at ~/.dotpack/installs.yaml — the parent dir
	// may not exist on first install. Upsert must mkdir -p.
	root := t.TempDir()
	store := manifest.NewStore(filepath.Join(root, "nested", "deep", "installs.yaml"))
	if err := store.Upsert(manifest.Record{ID: "x", InstalledAt: "now"}); err != nil {
		t.Fatalf("Upsert should create parent dir; got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nested", "deep", "installs.yaml")); err != nil {
		t.Errorf("manifest file not created: %v", err)
	}
}

func TestStore_TwoUpserts_DifferentIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	if err := store.Upsert(manifest.Record{ID: "a", InstalledAt: "t1"}); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}
	if err := store.Upsert(manifest.Record{ID: "b", InstalledAt: "t2"}); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}
	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Installs) != 2 {
		t.Fatalf("len(Installs): got %d, want 2", len(m.Installs))
	}
	if m.Installs[0].ID != "a" || m.Installs[1].ID != "b" {
		t.Errorf("order/ids: got %v, want [a, b]", []string{m.Installs[0].ID, m.Installs[1].ID})
	}
}

func TestStore_UpsertReplacesRecordWithSameID(t *testing.T) {
	// Slice 2 task #4: re-installing a resource must NOT append a
	// duplicate manifest record. The ID `host:kind:name` is unique per
	// install slot; the second Upsert replaces the first record's
	// content in place (new InstalledAt, new CacheKey, new Files list).
	// Per ADR-0008 the manifest is the reconcile source of truth, and
	// dedupe is the precondition for #5 (uninstall + list).
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)

	first := manifest.Record{ID: "claude-code:skill:foo", InstalledAt: "t1", CacheKey: "sha256:aaa"}
	second := manifest.Record{ID: "claude-code:skill:foo", InstalledAt: "t2", CacheKey: "sha256:bbb"}

	if err := store.Upsert(first); err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	if err := store.Upsert(second); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Installs) != 1 {
		t.Fatalf("len(Installs): got %d, want 1 (same ID must dedupe)", len(m.Installs))
	}
	got := m.Installs[0]
	if got.InstalledAt != second.InstalledAt {
		t.Errorf("InstalledAt: got %q, want %q (replacement record's value)", got.InstalledAt, second.InstalledAt)
	}
	if got.CacheKey != second.CacheKey {
		t.Errorf("CacheKey: got %q, want %q (replacement record's value)", got.CacheKey, second.CacheKey)
	}
}

func TestStore_UpsertRejectsEmptyID(t *testing.T) {
	// Hostile-review #3: empty IDs silently conflate. Upsert(Record{ID:""})
	// twice would collapse into one record; if a future kind's
	// resourceName() falls through to "" (default branch in
	// orchestrator.resourceName), every unnamed install of that kind
	// silently replaces the prior one. Reject at the manifest layer —
	// the manifest is the reconcile source of truth (ADR-0008) and
	// must not store unidentifiable rows.
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	err := store.Upsert(manifest.Record{InstalledAt: "now"})
	if err == nil {
		t.Fatal("expected Upsert to reject empty ID, got nil")
	}
	if !strings.Contains(err.Error(), "ID") {
		t.Errorf("error should name the missing field; got %v", err)
	}
}

func TestStore_RemoveExistingRecord(t *testing.T) {
	// Slice 3 task #5: Remove is the inverse of Upsert. Removing the
	// sole record in the manifest leaves an empty Installs slice — the
	// file remains (so subsequent loads see an empty manifest, not a
	// missing-file fallback) and the YAML round-trips.
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	if err := store.Upsert(manifest.Record{ID: "claude-code:skill:foo", InstalledAt: "t1"}); err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}
	if err := store.Remove("claude-code:skill:foo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Installs) != 0 {
		t.Errorf("len(Installs): got %d, want 0", len(m.Installs))
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("manifest file should still exist after Remove; stat: %v", err)
	}
}

func TestStore_RemovePreservesOrderOfOtherRecords(t *testing.T) {
	// Slot stability under Remove mirrors Upsert's slot preservation
	// (see TestStore_UpsertPreservesOrderOfOtherRecords): list output
	// must not jitter when an unrelated install is uninstalled.
	// Layout: [a, b, c] → Remove(b) → [a, c] (NOT [b, a, c] or any
	// other re-ordering).
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	for _, id := range []string{"a", "b", "c"} {
		if err := store.Upsert(manifest.Record{ID: id, InstalledAt: "t0"}); err != nil {
			t.Fatalf("seed Upsert %q: %v", id, err)
		}
	}
	if err := store.Remove("b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Installs) != 2 {
		t.Fatalf("len(Installs): got %d, want 2", len(m.Installs))
	}
	if m.Installs[0].ID != "a" || m.Installs[1].ID != "c" {
		t.Errorf("ordering: got %v, want [a, c]", []string{m.Installs[0].ID, m.Installs[1].ID})
	}
}

func TestStore_RemoveMissingID_Errors(t *testing.T) {
	// Loud failure when the caller asks to remove an ID that isn't in
	// the manifest. The CLI uses this to render "no such install" to
	// the user — silent no-op would mask typos and concurrent uninstalls.
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	if err := store.Upsert(manifest.Record{ID: "exists", InstalledAt: "t0"}); err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}
	err := store.Remove("does-not-exist")
	if err == nil {
		t.Fatal("expected Remove of missing ID to error, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should name the missing ID; got %v", err)
	}
	// Existing records still present (Remove did not mutate the file).
	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Installs) != 1 || m.Installs[0].ID != "exists" {
		t.Errorf("Installs after failed Remove: got %v, want [exists]", m.Installs)
	}
}

func TestStore_RemoveRejectsEmptyID(t *testing.T) {
	// Symmetry with Upsert (hostile-review #3): empty IDs silently
	// matching any future bug that produces an "" record would let
	// Remove("") nuke unidentifiable rows. Loud reject at the manifest
	// layer.
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	err := store.Remove("")
	if err == nil {
		t.Fatal("expected Remove with empty ID to error, got nil")
	}
	if !strings.Contains(err.Error(), "ID") {
		t.Errorf("error should name the missing field; got %v", err)
	}
}

func TestStore_UpsertPreservesOrderOfOtherRecords(t *testing.T) {
	// Advisor edge case: when an Upsert replaces an existing record,
	// the replacement must occupy the original slot rather than getting
	// bumped to the tail. Otherwise list views in #5 jitter on every
	// re-install. Layout: [a, b, c] → re-upsert(b) → still [a, b, c].
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	for _, id := range []string{"a", "b", "c"} {
		if err := store.Upsert(manifest.Record{ID: id, InstalledAt: "t0"}); err != nil {
			t.Fatalf("seed Upsert %q: %v", id, err)
		}
	}

	if err := store.Upsert(manifest.Record{ID: "b", InstalledAt: "t1-replacement"}); err != nil {
		t.Fatalf("replace Upsert: %v", err)
	}

	m, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Installs) != 3 {
		t.Fatalf("len(Installs): got %d, want 3", len(m.Installs))
	}
	gotOrder := []string{m.Installs[0].ID, m.Installs[1].ID, m.Installs[2].ID}
	wantOrder := []string{"a", "b", "c"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Errorf("ordering: got %v, want %v (replaced record must stay in slot)", gotOrder, wantOrder)
		}
	}
	if m.Installs[1].InstalledAt != "t1-replacement" {
		t.Errorf("middle record's InstalledAt: got %q, want %q (replacement value)", m.Installs[1].InstalledAt, "t1-replacement")
	}
}
