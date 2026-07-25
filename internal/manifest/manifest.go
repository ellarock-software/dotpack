// Package manifest persists install provenance to ~/.dotpack/installs.yaml,
// the source of truth for uninstall / list / reconcile / prune per ADR-0004.
// Load + Upsert + Remove are intentionally small primitives; higher-level
// workflows decide when a record is stale enough to remove.
package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"
)

// MergedKey is one (file, path) tuple the install merged into a host
// config file (per ADR-0012 §9). File is the absolute path of the host
// config file (e.g., <ProjectHome>/.mcp.json); Path is the format-native
// expression (JSONPath-ish "$.mcpServers.github" for JSON, dotted
// "mcp_servers.github" for TOML when codex lands) — the format is
// inferred from File's extension at apply / un-merge time so a path-
// shape mismatch is caught at the format-walker layer, not silently.
//
// Op + Selector encode the array-append case for the hook kind (and
// any future kind whose host config target is an array, not a leaf):
//
//   - Op="" (the default — leaf-set; mcp-server's shape): the Path
//     names a leaf slot; install overwrites; uninstall deletes the
//     leaf. Selector is empty.
//   - Op="append": the Path names an ARRAY container; install appends
//     Value; the orchestrator computes Selector as
//     "sha256:<hex(json.Marshal(Value))>" at install time and
//     persists it. Uninstall scans the array at Path, hashes each
//     element, and removes the one whose hash matches Selector —
//     stable across sibling installs/uninstalls and user reorders
//     (per advisor: numeric indices are unstable; content-hash is
//     the right identity). Drift-on-uninstall principle: if the user
//     edited the dotpack-installed element so the hash no longer
//     matches, uninstall is a no-op (leave the user's edit alone).
//
// Why a struct (vs the prior []string `"file#path"` shape this slice
// replaces): separators safe in both halves are not universal (# is
// rare in filenames but legal — macOS allows it), and uninstall
// genuinely needs the file path tuple to drive its un-merge step
// without re-deriving from the host or the schema. The placeholder
// []string carried no production data, so the format change is
// one-shot before adoption. Op + Selector were added with the hook
// slice; the YAML omitempty keeps mcp-server records visually
// unchanged on disk.
type MergedKey struct {
	File     string `yaml:"file"`
	Path     string `yaml:"path"`
	Op       string `yaml:"op,omitempty"`
	Selector string `yaml:"selector,omitempty"`
	SHA256   string `yaml:"sha256,omitempty"`
}

// FileClaim records the content hash dotpack wrote for one file output.
// Record.Files is retained for backwards-compatible uninstall/list code;
// FileClaims is the richer drift-detection shape used by inventory and
// daily reset/reinstall workflows.
type FileClaim struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256,omitempty"`
}

// Record is one install. Field set per ADR-0004's "{ id, source, kind,
// agent, scope, target_dir, files: [...], merged_keys: [...], cache_key,
// installed_at }" schema.
type Record struct {
	ID            string      `yaml:"id"`
	Source        string      `yaml:"source"`
	Kind          string      `yaml:"kind"`
	Agent         string      `yaml:"agent"`
	Scope         string      `yaml:"scope"`
	CanonicalRoot string      `yaml:"canonical_root,omitempty"`
	TargetRoot    string      `yaml:"target_root,omitempty"`
	SourcePath    string      `yaml:"source_path,omitempty"`
	SourceRelPath string      `yaml:"source_rel_path,omitempty"`
	SourceSHA256  string      `yaml:"source_sha256,omitempty"`
	TargetDir     string      `yaml:"target_dir,omitempty"`
	Files         []string    `yaml:"files,omitempty"`
	FileClaims    []FileClaim `yaml:"file_claims,omitempty"`
	MergedKeys    []MergedKey `yaml:"merged_keys,omitempty"`
	CacheKey      string      `yaml:"cache_key,omitempty"`
	InstalledAt   string      `yaml:"installed_at"`
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

// IdentityKey returns the manifest install slot key. Legacy records only
// have ID, so they keep the historic one-record-per-ID behavior. Records
// with target metadata include scope + target root so the same host/kind/name
// can be installed into multiple projects without overwriting provenance.
func IdentityKey(rec Record) string {
	if rec.TargetRoot == "" {
		return rec.ID
	}
	return rec.Scope + "|" + rec.TargetRoot + "|" + rec.ID
}

// SameIdentity reports whether two records address the same install slot.
func SameIdentity(a, b Record) bool {
	return IdentityKey(a) == IdentityKey(b)
}

// Upsert loads the current manifest, replaces any record whose install slot
// matches rec (preserving its slot in the slice), or appends rec
// if no match is found. The result is written back atomically (write
// to tmp, rename). Parent dirs are created if missing.
//
// Per ADR-0004 the manifest is the reconcile source of truth: the
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
		if SameIdentity(m.Installs[i], rec) {
			m.Installs[i] = rec
			return s.save(m)
		}
	}
	m.Installs = append(m.Installs, rec)
	return s.save(m)
}

// Remove loads the current manifest, deletes the record whose ID matches
// id (preserving the slot order of the surviving records), and writes
// the result back atomically. The manifest file itself is not deleted
// even when its Installs slice becomes empty — subsequent Load calls
// see an empty manifest cleanly rather than the "missing file" fallback.
//
// Errors loudly when id is not present: the CLI surfaces this as
// "no such install" so typos and concurrent uninstalls are visible
// rather than silently no-ops. Empty ID is rejected for the same
// reason Upsert rejects it (symmetry against any future code path
// that produces an empty ID — manifest must not address rows by "").
//
// Concurrency: NOT process-safe, same caveat as Upsert. Two concurrent
// `dotpack uninstall` invocations can lose work; file-lock is a slice-3
// concern when the feature surfaces.
func (s *Store) Remove(id string) error {
	if id == "" {
		return fmt.Errorf("manifest: Remove with empty ID is rejected (would address unidentifiable records)")
	}
	m, err := s.Load()
	if err != nil {
		return err
	}
	match := -1
	for i := range m.Installs {
		if m.Installs[i].ID != id {
			continue
		}
		if match >= 0 {
			return fmt.Errorf("manifest: install ID %q is ambiguous across target roots; use a target-scoped operation", id)
		}
		match = i
	}
	if match >= 0 {
		m.Installs = append(m.Installs[:match], m.Installs[match+1:]...)
		return s.save(m)
	}
	return fmt.Errorf("manifest: no install with ID %q", id)
}

// RemoveRecord deletes exactly the install slot represented by rec. This is
// used by target-scoped reset/prune flows where duplicate user-facing IDs can
// exist across different projects.
func (s *Store) RemoveRecord(rec Record) error {
	if rec.ID == "" {
		return fmt.Errorf("manifest: RemoveRecord with empty ID is rejected")
	}
	m, err := s.Load()
	if err != nil {
		return err
	}
	for i := range m.Installs {
		// A target-scoped reader can select a legacy record whose display
		// ID is duplicated elsewhere in the global manifest. Legacy
		// IdentityKey intentionally contains only ID, which is insufficient
		// for deletion: it could remove an unrelated legacy row rather than
		// the record just reconciled. RemoveRecord receives the fully loaded
		// record, so structural equality identifies the exact slot.
		if reflect.DeepEqual(m.Installs[i], rec) {
			m.Installs = append(m.Installs[:i], m.Installs[i+1:]...)
			return s.save(m)
		}
	}
	return fmt.Errorf("manifest: no install with identity %q", IdentityKey(rec))
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
