// Package orchestrator applies an adapter's InstallPlan to the
// filesystem and persists the manifest record. Per ADR-0016 §2 the
// orchestrator owns: filesystem mutation, manifest construction, and
// the --allow-lossy gate. Adapters return plans; the orchestrator
// runs them.
package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/manifest"
	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
)

// Orchestrator wires the dirs, an Adapter, and the manifest store.
// One Orchestrator handles one (host, manifest) pair; --agent agents-cli
// fan-out (ADR-0016 §1) is a higher layer that constructs multiple
// orchestrators.
type Orchestrator struct {
	dirs     dirs.Dirs
	adapter  adapter.Adapter
	manifest *manifest.Store
	now      func() time.Time // injected for deterministic tests if needed
}

// New constructs an Orchestrator.
func New(d dirs.Dirs, a adapter.Adapter, m *manifest.Store) *Orchestrator {
	return &Orchestrator{dirs: d, adapter: a, manifest: m, now: func() time.Time { return time.Now().UTC() }}
}

// InstallOptions are the per-call knobs (analogous to CLI flags). Source
// is the URI the resource came from (file:// for local paths, git URL
// for upstream).
type InstallOptions struct {
	Source     string
	AllowLossy bool
	// Force bypasses the pre-flight collision check (ADR-0008 hygiene:
	// the orchestrator refuses to overwrite untracked files at an
	// install target). Use only when the user has audited the
	// collision and accepts the overwrite — mirrors --allow-lossy.
	// The CLI surfaces this as --force.
	Force bool
}

// InstallResult is what Install returns on success — the plan that was
// applied, the manifest Record that was persisted, and any LossyReasons
// the user opted into via --allow-lossy. The reasons are surfaced even
// on success so the CLI can echo "installed but the following fields
// were dropped: ..." for audit trails.
type InstallResult struct {
	Plan         adapter.InstallPlan
	Record       manifest.Record
	LossyReasons []adapter.LossyReason
}

// LossyError is returned when the plan is lossy and AllowLossy is false.
// Callers (the CLI) match on this to print the lossy-fields list and
// suggest --allow-lossy.
type LossyError struct {
	Host    string
	Reasons []adapter.LossyReason
}

// CollisionError is returned when the plan would write at one or more
// paths that already exist on disk AND the install's ID is not present
// in the manifest. Refusing rather than overwriting protects
// user-edited files (on FIRST install) and orphans from a previously-
// failed install. --force on the CLI maps to InstallOptions.Force and
// bypasses.
//
// Drift gap (hostile-review #6, deferred): once an install IS in the
// manifest, re-install silently overwrites the on-disk bytes even if
// the user edited them externally. Cache_key drift detection would
// catch this but isn't wired up yet — slice 3 concern. The current
// protection is one-shot: first-install collisions are blocked;
// subsequent in-place updates trust the manifest.
type CollisionError struct {
	Paths []string
}

// Error renders the colliding paths plus the --force hint, matching
// LossyError's "say what's wrong and how to proceed" shape so a single
// error message is actionable without re-running.
func (e *CollisionError) Error() string {
	lines := make([]string, 0, len(e.Paths)+2)
	lines = append(lines, "install would collide with existing untracked files:")
	for _, p := range e.Paths {
		lines = append(lines, fmt.Sprintf("  - %s", p))
	}
	lines = append(lines, "pass --force to overwrite")
	return strings.Join(lines, "\n")
}

// Error renders the lossy reasons with the diagnostic data the §8
// algorithm collected (concept + supporting hosts), so the user knows
// WHY each field was rejected and WHERE it would have worked — not
// just a bare field-name list. Format example:
//
//	install would be lossy on gemini:
//	  - allowed-tools (concept: claude_skill_runtime_overrides; native on: claude-code)
//	  - my_typo_field (unknown field — no schema entry claims it)
//	pass --allow-lossy to proceed
func (e *LossyError) Error() string {
	lines := make([]string, 0, len(e.Reasons)+2)
	lines = append(lines, fmt.Sprintf("install would be lossy on %s:", e.Host))
	for _, r := range e.Reasons {
		switch {
		case r.CanonicalConcept == "":
			lines = append(lines, fmt.Sprintf("  - %s (unknown field — no schema entry claims it)", r.FieldPath))
		case len(r.SupportedHosts) == 0:
			lines = append(lines, fmt.Sprintf("  - %s (concept: %s; no host natively supports it)", r.FieldPath, r.CanonicalConcept))
		default:
			lines = append(lines, fmt.Sprintf("  - %s (concept: %s; native on: %s)",
				r.FieldPath, r.CanonicalConcept, strings.Join(r.SupportedHosts, ", ")))
		}
	}
	lines = append(lines, "pass --allow-lossy to proceed")
	return strings.Join(lines, "\n")
}

// Install runs the adapter's Plan against the resource, applies file
// writes to disk, and appends a manifest record. Per-instance lossy
// detection (ADR-0016 §8) is computed here from the schema against
// the resource's Extensions — the adapter's Plan does not carry lossy
// state; the schema is the single source of truth.
func (o *Orchestrator) Install(r resource.Resource, scope adapter.Scope, opts InstallOptions) (InstallResult, error) {
	plan, err := o.adapter.Plan(r, scope)
	if err != nil {
		return InstallResult{}, fmt.Errorf("plan: %w", err)
	}

	reasons, err := schema.LossyExtensions(r.Kind(), o.adapter.HostID(), r.Extensions())
	if err != nil {
		return InstallResult{}, fmt.Errorf("lossy check: %w", err)
	}
	if len(reasons) > 0 && !opts.AllowLossy {
		return InstallResult{}, &LossyError{Host: o.adapter.HostID(), Reasons: reasons}
	}

	rec := buildRecord(o.adapter.HostID(), r, scope, plan, opts, o.now())

	if !opts.Force {
		collisions, err := preflightCollisions(o.manifest, rec.ID, plan.Files)
		if err != nil {
			return InstallResult{}, fmt.Errorf("collision check: %w", err)
		}
		if len(collisions) > 0 {
			return InstallResult{}, &CollisionError{Paths: collisions}
		}
	}

	// Partial-write orphan handling deferred: if file K of N fails
	// mid-loop, files 1..K-1 are on disk with no manifest record.
	// Pre-flight on re-install will (correctly) flag them as collisions
	// so the user can recover via --force, but actively cleaning up
	// the orphans here is #5's job (uninstall semantics).
	for _, fw := range plan.Files {
		if err := writeAtomic(fw); err != nil {
			return InstallResult{}, fmt.Errorf("apply file %s: %w", fw.Path, err)
		}
	}

	if err := o.manifest.Upsert(rec); err != nil {
		return InstallResult{}, fmt.Errorf("upsert manifest: %w", err)
	}

	return InstallResult{Plan: plan, Record: rec, LossyReasons: reasons}, nil
}

// preflightCollisions returns paths in `files` that exist on disk but
// are NOT owned by an existing manifest record for id. Owned paths
// (re-install case) are allowed; unknown paths (user-edited files or
// orphans from a partial prior install) are reported. Returning a
// non-nil error here means the manifest itself could not be read —
// surfaced separately from CollisionError so the caller can tell
// "your filesystem has unexpected state" apart from "your manifest
// is broken".
//
// Uses os.Lstat (not os.Stat) so symlinks at the target path ARE
// detected as collisions even when their target is missing (dangling)
// or unrelated. Treating a symlink as "no collision" would silently
// replace the user's indirection with a regular file on --force.
//
// TOCTOU caveat: this is a pre-flight check, not an atomic guard.
// A path created between this stat and writeAtomic's rename will
// be overwritten. The slice-2 hardening targets passive state
// (untracked files / orphans), not concurrent races.
func preflightCollisions(store *manifest.Store, id string, files []adapter.FileWrite) ([]string, error) {
	m, err := store.Load()
	if err != nil {
		return nil, err
	}
	for _, rec := range m.Installs {
		if rec.ID == id {
			// Known install: re-install is allowed. Skip the stat
			// loop entirely — overwriting our own files is the
			// expected update path.
			return nil, nil
		}
	}
	var collisions []string
	for _, fw := range files {
		if _, err := os.Lstat(fw.Path); err == nil {
			collisions = append(collisions, fw.Path)
		}
	}
	return collisions, nil
}

// writeAtomic ensures parent dirs exist, writes the file to a tmp path
// in the target directory, and renames into place. The rename is the
// atomic-publish step; a crash mid-write leaves the prior file intact
// (or no file at all on first install).
func writeAtomic(fw adapter.FileWrite) error {
	if err := os.MkdirAll(filepath.Dir(fw.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(fw.Path), filepath.Base(fw.Path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(fw.Content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	mode := fw.Mode
	if mode == 0 {
		mode = 0o644
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, fw.Path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// buildRecord composes a manifest.Record from the install context. ID
// is host:kind:name, suitable as a uniqueness key within one host.
func buildRecord(host string, r resource.Resource, scope adapter.Scope, plan adapter.InstallPlan, opts InstallOptions, when time.Time) manifest.Record {
	name := resourceName(r)
	files := make([]string, 0, len(plan.Files))
	for _, f := range plan.Files {
		files = append(files, f.Path)
	}
	sort.Strings(files)

	var targetDir string
	if len(plan.Files) > 0 {
		targetDir = filepath.Dir(plan.Files[0].Path)
	}

	return manifest.Record{
		ID:          fmt.Sprintf("%s:%s:%s", host, r.Kind(), name),
		Source:      opts.Source,
		Kind:        string(r.Kind()),
		Agent:       host,
		Scope:       string(scope),
		TargetDir:   targetDir,
		Files:       files,
		CacheKey:    cacheKey(plan),
		InstalledAt: when.Format(time.RFC3339),
	}
}

// resourceName extracts the human-readable name field from a Resource.
// Per-kind for now; a Named interface would generalise once a second
// kind needs this.
func resourceName(r resource.Resource) string {
	switch v := r.(type) {
	case *resource.Skill:
		return v.Name
	default:
		return ""
	}
}

// cacheKey hashes the install plan's file contents to give the manifest
// a deduplication / drift-detection handle. Per ADR-0008 the manifest
// carries a cache_key field; in slice 1 the cache layer doesn't yet
// exist, so we synthesise the key from the bytes the adapter emitted.
func cacheKey(plan adapter.InstallPlan) string {
	h := sha256.New()
	for _, f := range plan.Files {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write(f.Content)
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
