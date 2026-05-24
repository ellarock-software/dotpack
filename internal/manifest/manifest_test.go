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

func TestStore_AppendThenLoadRoundtrip(t *testing.T) {
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
	if err := store.Append(rec); err != nil {
		t.Fatalf("Append: %v", err)
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

func TestStore_AppendIsAtomic_NoLingeringTempFile(t *testing.T) {
	// Per ADR-0008: write atomically (write to tmp, rename). After
	// Append returns, no .tmp scratch files should remain in the
	// manifest's directory.
	tmp := t.TempDir()
	store := manifest.NewStore(filepath.Join(tmp, "installs.yaml"))
	if err := store.Append(manifest.Record{ID: "x", InstalledAt: "now"}); err != nil {
		t.Fatalf("Append: %v", err)
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

func TestStore_AppendCreatesParentDir(t *testing.T) {
	// installs.yaml lives at ~/.dotpack/installs.yaml — the parent dir
	// may not exist on first install. Append must mkdir -p.
	root := t.TempDir()
	store := manifest.NewStore(filepath.Join(root, "nested", "deep", "installs.yaml"))
	if err := store.Append(manifest.Record{ID: "x", InstalledAt: "now"}); err != nil {
		t.Fatalf("Append should create parent dir; got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nested", "deep", "installs.yaml")); err != nil {
		t.Errorf("manifest file not created: %v", err)
	}
}

func TestStore_TwoAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installs.yaml")
	store := manifest.NewStore(path)
	if err := store.Append(manifest.Record{ID: "a", InstalledAt: "t1"}); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := store.Append(manifest.Record{ID: "b", InstalledAt: "t2"}); err != nil {
		t.Fatalf("Append 2: %v", err)
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
