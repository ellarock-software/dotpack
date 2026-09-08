package sourceregistry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManagedSkill records the state of an individual skill installed from a repository.
type ManagedSkill struct {
	Name          string `yaml:"name"`
	SourceRelPath string `yaml:"source_rel_path"`
	AppliedCommit string `yaml:"applied_commit"`
	ContentSHA256 string `yaml:"content_sha256"`
}

// TargetBinding records the installation target configuration for a repository.
type TargetBinding struct {
	Agent           string         `yaml:"agent"`
	Scope           string         `yaml:"scope"`
	TargetRoot      string         `yaml:"target_root,omitempty"`
	AppliedCommit   string         `yaml:"applied_commit,omitempty"`
	InstalledSkills []ManagedSkill `yaml:"installed_skills,omitempty"`
}

// TargetBindingKey returns a unique key for comparing target destinations.
func TargetBindingKey(t TargetBinding) string {
	return t.Agent + "|" + t.Scope + "|" + filepath.Clean(t.TargetRoot)
}

// SameTarget reports whether two bindings address the same target destination.
func SameTarget(a, b TargetBinding) bool {
	return TargetBindingKey(a) == TargetBindingKey(b)
}

// SourceRecord is one registered repository subscription.
type SourceRecord struct {
	ID                string          `yaml:"id"`
	URL               string          `yaml:"url"`
	Ref               string          `yaml:"ref,omitempty"`
	LastFetchedCommit string          `yaml:"last_fetched_commit,omitempty"`
	DiscoveryPath     string          `yaml:"discovery_path,omitempty"`
	Targets           []TargetBinding `yaml:"targets,omitempty"`
}

// Registry is the top-level YAML structure of sources.yaml.
type Registry struct {
	Version int            `yaml:"version"`
	Sources []SourceRecord `yaml:"sources"`
}

// Store reads and writes sources.yaml.
type Store struct {
	path string
}

// NewStore constructs a Store for a given path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path returns the filesystem path of the registry file.
func (s *Store) Path() string {
	return s.path
}

// Load reads sources.yaml, returning an empty Registry if the file does not exist.
func (s *Store) Load() (*Registry, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Registry{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sourceregistry: read %s: %w", s.path, err)
	}
	reg := &Registry{}
	if err := yaml.Unmarshal(raw, reg); err != nil {
		return nil, fmt.Errorf("sourceregistry: parse %s: %w", s.path, err)
	}
	if reg.Version == 0 {
		reg.Version = 1
	}
	return reg, nil
}

// GetSource looks up a source record by ID.
func (s *Store) GetSource(id string) (*SourceRecord, bool, error) {
	reg, err := s.Load()
	if err != nil {
		return nil, false, err
	}
	for _, src := range reg.Sources {
		if strings.EqualFold(src.ID, id) {
			copy := src
			return &copy, true, nil
		}
	}
	return nil, false, nil
}

// UpsertSource adds or updates a SourceRecord.
// If a source with the same ID already exists:
// - Its URL, Ref, DiscoveryPath, and LastFetchedCommit are updated (if provided).
// - Its TargetBindings are merged by TargetBindingKey.
// If not, it is appended.
func (s *Store) UpsertSource(src SourceRecord) error {
	if strings.TrimSpace(src.ID) == "" {
		return fmt.Errorf("sourceregistry: UpsertSource with empty ID is rejected")
	}
	reg, err := s.Load()
	if err != nil {
		return err
	}

	found := false
	for i := range reg.Sources {
		if strings.EqualFold(reg.Sources[i].ID, src.ID) {
			found = true
			existing := &reg.Sources[i]
			if src.URL != "" {
				existing.URL = src.URL
			}
			existing.Ref = src.Ref
			if src.DiscoveryPath != "" {
				existing.DiscoveryPath = src.DiscoveryPath
			}
			if src.LastFetchedCommit != "" {
				existing.LastFetchedCommit = src.LastFetchedCommit
			}
			// Merge targets
			for _, newTarget := range src.Targets {
				targetFound := false
				for ti := range existing.Targets {
					if SameTarget(existing.Targets[ti], newTarget) {
						targetFound = true
						if newTarget.AppliedCommit != "" {
							existing.Targets[ti].AppliedCommit = newTarget.AppliedCommit
						}
						if len(newTarget.InstalledSkills) > 0 {
							existing.Targets[ti].InstalledSkills = newTarget.InstalledSkills
						}
						break
					}
				}
				if !targetFound {
					existing.Targets = append(existing.Targets, newTarget)
				}
			}
			break
		}
	}

	if !found {
		reg.Sources = append(reg.Sources, src)
	}

	return s.Save(reg)
}

// RemoveSource removes a repository subscription by ID.
func (s *Store) RemoveSource(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("sourceregistry: RemoveSource with empty ID is rejected")
	}
	reg, err := s.Load()
	if err != nil {
		return err
	}
	match := -1
	for i := range reg.Sources {
		if strings.EqualFold(reg.Sources[i].ID, id) {
			match = i
			break
		}
	}
	if match < 0 {
		return fmt.Errorf("sourceregistry: no source with ID %q", id)
	}
	reg.Sources = append(reg.Sources[:match], reg.Sources[match+1:]...)
	return s.Save(reg)
}

// Save writes the registry atomically to disk.
func (s *Store) Save(reg *Registry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("sourceregistry: mkdir %s: %w", filepath.Dir(s.path), err)
	}
	raw, err := yaml.Marshal(reg)
	if err != nil {
		return fmt.Errorf("sourceregistry: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".sources.*.tmp")
	if err != nil {
		return fmt.Errorf("sourceregistry: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sourceregistry: write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sourceregistry: fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("sourceregistry: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		cleanup()
		return fmt.Errorf("sourceregistry: rename %s -> %s: %w", tmpPath, s.path, err)
	}
	return nil
}
