package manifest

import (
	"path/filepath"
	"testing"
)

func TestRemoveRecordDistinguishesDuplicateLegacyIDs(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "installs.yaml"))
	a := Record{
		ID:     "codex:hook:portable-registry",
		Scope:  "project",
		Source: "file:///tmp/project-a/portable-registry.json",
		MergedKeys: []MergedKey{{
			File: "/tmp/project-a/.codex/config.toml", Path: "hooks.Stop", Op: "append", Selector: "sha256:a",
		}},
	}
	b := Record{
		ID:     "codex:hook:portable-registry",
		Scope:  "project",
		Source: "file:///tmp/project-b/portable-registry.json",
		MergedKeys: []MergedKey{{
			File: "/tmp/project-b/.codex/config.toml", Path: "hooks.Stop", Op: "append", Selector: "sha256:b",
		}},
	}
	if err := store.save(&Manifest{Installs: []Record{a, b}}); err != nil {
		t.Fatalf("seed duplicate legacy rows: %v", err)
	}
	if err := store.RemoveRecord(b); err != nil {
		t.Fatalf("RemoveRecord b: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load after remove: %v", err)
	}
	if len(got.Installs) != 1 || got.Installs[0].Source != a.Source {
		t.Fatalf("remaining records = %+v; want only a", got.Installs)
	}
}
