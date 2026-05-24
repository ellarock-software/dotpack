// Package manifest persists install provenance to ~/.dotpack/installs.yaml,
// the source of truth for uninstall / list / reconcile per ADR-0008.
// Slice 1 supports Load + Upsert; remove and reconcile arrive when their
// CLI subcommands do.
package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Record is one install. Field set per ADR-0008's "{ id, source, kind,
// agent, scope, target_dir, files: [...], merged_keys: [...], cache_key,
// installed_at }" schema.
type Record struct {
	ID          string   `yaml:"id"`
	Source      string   `yaml:"source"`
	Kind        string   `yaml:"kind"`
	Agent       string   `yaml:"agent"`
	Scope       string   `yaml:"scope"`
	TargetDir   string   `yaml:"target_dir,omitempty"`
	Files       []string `yaml:"files,omitempty"`
	MergedKeys  []string `yaml:"merged_keys,omitempty"`
	CacheKey    string   `yaml:"cache_key,omitempty"`
	InstalledAt string   `yaml:"installed_at"`
}

// Manifest is the top-level YAML structure of installs.yaml.
type Manifest struct {
	Installs []Record `yaml:"installs"`
}

// Store reads and writes one installs.yaml file. Construct via NewStore.
type Store struct {
	path string
}

// NewStore wraps a path. The file does not need to exist at construction;
// Load on a missing file returns an empty Manifest.
func NewStore(path string) *Store { return &Store{path: path} }

// Path returns the manifest's filesystem path.
func (s *Store) Path() string { return s.path }

// Load returns the manifest's current contents, or an empty Manifest if
// the file does not exist. Errors only on I/O or YAML-parse failures.
func (s *Store) Load() (*Manifest, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", s.path, err)
	}
	m := &Manifest{}
	if err := yaml.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("manifest: parse %s: %w", s.path, err)
	}
	return m, nil
}

// Upsert loads the current manifest, replaces any record whose ID
// matches rec.ID (preserving its slot in the slice), or appends rec
// if no match is found. The result is written back atomically (write
// to tmp, rename). Parent dirs are created if missing.
//
// Per ADR-0008 the manifest is the reconcile source of truth: the
// (host, kind, name) tuple — encoded as the ID — uniquely identifies
// an install slot. Re-installs MUST be idempotent at the record level
// so uninstall/list/reconcile behave consistently. Slot preservation
// keeps `dotpack list` output stable across re-installs.
//
// Empty IDs are rejected (hostile-review safeguard): if a future kind
// fell through orchestrator.resourceName's default branch and produced
// an empty ID, every unnamed install would silently overwrite the
// prior one. The manifest must not store unidentifiable rows.
//
// Concurrency: NOT process-safe. Two concurrent `dotpack install`
// invocations can both Load → both append → last writer wins,
// silently losing one install's record. Single-process serialisation
// is sufficient for slice 2 (no concurrent-install feature); a file
// lock or single-writer daemon is the slice-3 concern when needed.
func (s *Store) Upsert(rec Record) error {
	if rec.ID == "" {
		return fmt.Errorf("manifest: Upsert with empty ID is rejected (would conflate unidentifiable records)")
	}
	m, err := s.Load()
	if err != nil {
		return err
	}
	for i := range m.Installs {
		if m.Installs[i].ID == rec.ID {
			m.Installs[i] = rec
			return s.save(m)
		}
	}
	m.Installs = append(m.Installs, rec)
	return s.save(m)
}

// save serialises m and writes it atomically over s.path.
func (s *Store) save(m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("manifest: mkdir parent of %s: %w", s.path, err)
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("manifest: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".installs.*.tmp")
	if err != nil {
		return fmt.Errorf("manifest: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("manifest: write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("manifest: fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("manifest: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		cleanup()
		return fmt.Errorf("manifest: rename %s -> %s: %w", tmpPath, s.path, err)
	}
	return nil
}
