// Package manifest persists install provenance to ~/.dotpack/installs.yaml,
// the source of truth for uninstall / list / reconcile per ADR-0008.
// Slice 1 supports Load + Append; remove and reconcile arrive when their
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

// Append loads the current manifest, appends rec, and writes the result
// back atomically (write to tmp, rename). Parent dirs are created if
// missing. Concurrent Appends from a single process are not synchronised
// here — that's the orchestrator's lock concern if it ever needs one.
func (s *Store) Append(rec Record) error {
	m, err := s.Load()
	if err != nil {
		return err
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
