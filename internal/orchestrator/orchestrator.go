// Package orchestrator applies an adapter's InstallPlan to the
// filesystem and persists the manifest record. Per ADR-0016 §2 the
// orchestrator owns: filesystem mutation, manifest construction, and
// the --allow-lossy gate. Adapters return plans; the orchestrator
// runs them.
//
// Two types split the surface by adapter dependency:
//
//   - Installer (NewInstaller): holds an Adapter; method Install runs the
//     adapter's Plan and applies it.
//   - Reader (NewReader): no adapter; methods List + Uninstall work from
//     the manifest alone (manifest records carry absolute paths and the
//     host is encoded in the ID, so neither call needs an adapter today).
//
// The split replaces an earlier single Orchestrator type whose New(d, a, mf)
// accepted a nil adapter for List + Uninstall — honest about today's
// behaviour but actively misleading when ADR-0016 §9's un-merge-keys
// step lands and Uninstall starts needing the adapter. With the split,
// the §9 migration is a method move (Reader.Uninstall → Installer.Uninstall),
// not an API redesign.
package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Reader handles operations that DO NOT traverse an adapter: List and
// Uninstall. Both work from the manifest alone — List reads slot order;
// Uninstall walks the record's absolute Files paths and removes them
// AND walks the record's MergedKeys to un-merge config fragments from
// JSON/TOML host config files. Construct with NewReader(d, m). The
// CLI's `dotpack list` and `dotpack uninstall` use this type — neither
// needs to know about per-host adapter wiring.
//
// ADR-0016 §9 refinement (2026-05-25): the prior framing said
// "Uninstall will gain an un-merge step that requires the host adapter,
// at which point Uninstall moves to Installer". Empirically the
// un-merge is FORMAT-specific (JSON vs TOML walker), not host-specific
// — the manifest record's (File, Path) tuples are self-sufficient, so
// Reader stays adapter-free and `unmergeKey` dispatches by file
// extension. If a future slice surfaces a genuine adapter-need at
// uninstall time (e.g., a kind where the file path can't be stored
// canonically), the Reader→Installer migration path stays open. See
// internal/orchestrator/mergedkeys.go's package docstring for the full
// rationale.
type Reader struct {
	dirs     dirs.Dirs
	manifest *manifest.Store
}

// Installer handles Install. Holds the target adapter — the only
// operation that actually traverses it today. One Installer handles one
// (host, manifest) pair; --agent agents-cli fan-out (ADR-0016 §1) is a
// higher layer that constructs multiple Installers.
type Installer struct {
	dirs     dirs.Dirs
	adapter  adapter.Adapter
	manifest *manifest.Store
	now      func() time.Time // injected for deterministic tests if needed
}

// NewReader constructs a Reader. No adapter — List and Uninstall do not
// traverse one.
func NewReader(d dirs.Dirs, m *manifest.Store) *Reader {
	return &Reader{dirs: d, manifest: m}
}

// NewInstaller constructs an Installer. The Adapter is required; Install
// dispatches to adapter.Plan for the resource, so a nil adapter would
// panic at the first call site.
func NewInstaller(d dirs.Dirs, a adapter.Adapter, m *manifest.Store) *Installer {
	return &Installer{dirs: d, adapter: a, manifest: m, now: func() time.Time { return time.Now().UTC() }}
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
//	install would be lossy on gemini-cli:
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
func (i *Installer) Install(r resource.Resource, scope adapter.Scope, opts InstallOptions) (InstallResult, error) {
	plan, err := i.adapter.Plan(r, scope)
	if err != nil {
		return InstallResult{}, fmt.Errorf("plan: %w", err)
	}

	reasons, err := schema.LossyExtensions(r.Kind(), i.adapter.HostID(), r.Extensions())
	if err != nil {
		return InstallResult{}, fmt.Errorf("lossy check: %w", err)
	}
	if len(reasons) > 0 && !opts.AllowLossy {
		return InstallResult{}, &LossyError{Host: i.adapter.HostID(), Reasons: reasons}
	}

	rec, err := buildRecord(i.adapter.HostID(), r, scope, plan, opts, i.now())
	if err != nil {
		return InstallResult{}, err
	}

	if !opts.Force {
		collisions, err := preflightCollisions(i.manifest, rec.ID, plan.Files)
		if err != nil {
			return InstallResult{}, fmt.Errorf("collision check: %w", err)
		}
		if len(collisions) > 0 {
			return InstallResult{}, &CollisionError{Paths: collisions}
		}
		mkCollisions, err := preflightMergedKeyCollisions(i.manifest, rec.ID, plan.MergedKeys)
		if err != nil {
			return InstallResult{}, fmt.Errorf("merged-key collision check: %w", err)
		}
		if len(mkCollisions) > 0 {
			return InstallResult{}, &CollisionError{Paths: mkCollisions}
		}
	}

	// Partial-write orphan handling: if file K of N fails mid-loop,
	// files 1..K-1 are on disk with no manifest record. Pre-flight on
	// re-install will (correctly) flag them as collisions so the user can
	// recover via --force. Uninstall cannot close this gap — there's no
	// ID to look up for record-less files — and prune/reconcile stay
	// provenance-driven rather than guessing ownership of untracked paths.
	//
	// Same gap extends to merged keys (hostile-review #7): if merged-key
	// M+1 fails after files 0..K and merged keys 0..M succeeded, the
	// host config file has M+1 entries the manifest doesn't track.
	// Re-install preflight catches them; uninstall-by-ID and prune do not
	// remove unmarked config fragments.
	//
	// Re-install handling for Op=Append: if a record with the same ID
	// already exists, un-merge its existing Op=Append entries BEFORE
	// applying the new plan. Without this step a re-install of a hook
	// whose value changed would leave the OLD array element in place
	// and APPEND the new value — array would contain both. For Op=Set
	// the apply overwrites the leaf so no pre-clean is needed; we
	// scope the un-merge to Op=Append entries only. The manifest
	// upsert at the end of Install carries the new selectors so a
	// subsequent uninstall finds the new content.
	if err := unmergeExistingAppendsForID(i.manifest, rec.ID); err != nil {
		return InstallResult{}, fmt.Errorf("re-install cleanup: %w", err)
	}
	for _, fw := range plan.Files {
		if err := writeAtomic(fw); err != nil {
			return InstallResult{}, fmt.Errorf("apply file %s: %w", fw.Path, err)
		}
	}
	for _, rm := range plan.RemoveFiles {
		if err := removeStaleFile(rm); err != nil {
			return InstallResult{}, fmt.Errorf("remove stale file %s: %w", rm.Path, err)
		}
	}
	for _, mk := range plan.MergedKeys {
		if err := applyMergedKey(mk); err != nil {
			return InstallResult{}, fmt.Errorf("apply merged key %s in %s: %w", mk.Path, mk.File, err)
		}
	}

	if err := i.manifest.Upsert(rec); err != nil {
		return InstallResult{}, fmt.Errorf("upsert manifest: %w", err)
	}

	return InstallResult{Plan: plan, Record: rec, LossyReasons: reasons}, nil
}

// UninstallResult is what Uninstall returns on success. Per-path
// outcome is tracked truthfully (hostile-review #3 of slice 3 — the
// CLI used to print "removed X" even when X was already gone, which
// lied to the user about what dotpack actually did):
//
//   - RemovedPaths: files the orchestrator actually deleted from disk.
//   - MissingPaths: files the manifest knew about but were already
//     gone at uninstall time (manual rm, prior crashed uninstall, etc.)
//     — reported so the CLI can render "skipped (already gone)".
//   - TargetDirRemoved: true when the install's TargetDir was cleaned
//     up because it became empty; false when a sibling file kept it
//     non-empty (or it was empty in the record, etc.) so the CLI can
//     say "kept directory <path>".
type UninstallResult struct {
	Record           manifest.Record
	RemovedPaths     []string
	MissingPaths     []string
	TargetDirRemoved bool
}

// Uninstall is the inverse of Install: remove the files this install
// owns from disk, drop any TargetDir that became empty as a result,
// and remove the manifest record. Lookup is by ID (host:kind:name).
//
// Ordering: filesystem first, then manifest. A crash between the two
// leaves files gone with the record still present — a subsequent
// `dotpack uninstall <id>` is then a clean idempotent retry over the
// missing files (os.Remove returns fs.ErrNotExist, which we treat as
// "already done"). The reverse order would orphan files with no
// manifest handle, which `dotpack uninstall` could not address.
//
// Empty-dir cleanup boundary (advisor #2): we call os.Remove on
// rec.TargetDir exactly once. Non-empty → ENOTEMPTY → silently
// preserved. Never walk up past TargetDir; never RemoveAll. The user's
// sibling files (README.md beside SKILL.md, etc.) survive.
//
// Drift on uninstall is intentional (advisor #6): if the user edited
// the on-disk bytes, uninstall still removes the file. Intent is clear
// from "uninstall"; this is not symmetric with install's collision
// protection.
//
// Orphan cleanup boundary (advisor #4 + writeAtomic TODO): a partial
// install (file K of N written, then crash before manifest.Upsert)
// leaves files with no record. Uninstall-by-ID can NOT clean those up
// — there's no ID to look up. Reconcile/prune are manifest-backed and
// clean stale records whose claims disappeared; they deliberately do
// not delete record-less files or config fragments.
func (r *Reader) Uninstall(id string) (UninstallResult, error) {
	m, err := r.manifest.Load()
	if err != nil {
		return UninstallResult{}, fmt.Errorf("load manifest: %w", err)
	}
	var rec manifest.Record
	found := false
	for _, r := range m.Installs {
		if r.ID == id {
			rec = r
			found = true
			break
		}
	}
	if !found {
		// Helpful when the user composed the ID from short-name + flag
		// defaults: list any records sharing the trailing short-name so
		// "did you mean --kind agent?" is one glance away. Pre-agent
		// this footgun didn't exist; once agent kind shipped, a default
		// --kind=skill misroutes silently otherwise.
		short := id
		if i := strings.LastIndex(id, ":"); i >= 0 {
			short = id[i+1:]
		}
		var hints []string
		for _, r := range m.Installs {
			if r.ID == id {
				continue
			}
			rs := r.ID
			if i := strings.LastIndex(rs, ":"); i >= 0 {
				rs = rs[i+1:]
			}
			if rs == short {
				hints = append(hints, r.ID)
			}
		}
		if len(hints) > 0 {
			return UninstallResult{}, fmt.Errorf("no install with ID %q; did you mean one of: %s",
				id, strings.Join(hints, ", "))
		}
		return UninstallResult{}, fmt.Errorf("no install with ID %q", id)
	}

	var removed, missing []string
	for _, p := range rec.Files {
		err := os.Remove(p)
		switch {
		case err == nil:
			removed = append(removed, p)
		case os.IsNotExist(err):
			missing = append(missing, p)
		default:
			return UninstallResult{}, fmt.Errorf("remove %s: %w", p, err)
		}
	}

	// Config-fragment un-merge per ADR-0016 §9. Each manifest MergedKey
	// names the file the install merged into and the format-native path
	// it merged at; unmergeKey dispatches by file extension (JSON today;
	// TOML when codex lands) and atomic-writes the result. Per advisor
	// (refining ADR §9's "requires the host adapter" future-note), the
	// un-merge is format-specific, not host-specific — the manifest
	// record's (File, Path) tuple is self-sufficient, so Reader stays
	// adapter-free.
	//
	// Absent / empty target files and absent leaves are no-ops
	// (uninstall is idempotent across already-cleaned state); other
	// I/O or parse errors short-circuit with a wrapped error.
	for _, mk := range rec.MergedKeys {
		if err := unmergeKey(MergedKeySelector{
			File:     mk.File,
			Path:     mk.Path,
			Op:       adapter.MergedKeyOp(mk.Op),
			Selector: mk.Selector,
		}); err != nil {
			return UninstallResult{}, fmt.Errorf("un-merge key %s in %s: %w", mk.Path, mk.File, err)
		}
	}

	// Best-effort empty-dir cleanup. ENOTEMPTY → silently preserved
	// (user has sibling files we don't own). Any other error (perm,
	// etc.) is also tolerated — but we report dirRemoved=false so the
	// CLI doesn't lie about whether dotpack cleaned the dir up.
	dirRemoved := false
	if rec.TargetDir != "" {
		if err := os.Remove(rec.TargetDir); err == nil {
			dirRemoved = true
		}
	}

	if err := r.manifest.Remove(id); err != nil {
		return UninstallResult{}, fmt.Errorf("remove manifest record: %w", err)
	}

	return UninstallResult{
		Record:           rec,
		RemovedPaths:     removed,
		MissingPaths:     missing,
		TargetDirRemoved: dirRemoved,
	}, nil
}

// List returns the manifest records in slot order. Thin wrapper around
// manifest.Store.Load — formatting is the CLI's concern. Missing
// manifest file → empty slice, not an error (slice 1 contract: a fresh
// system has no installs yet).
func (r *Reader) List() ([]manifest.Record, error) {
	m, err := r.manifest.Load()
	if err != nil {
		return nil, err
	}
	return m.Installs, nil
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

func removeStaleFile(rm adapter.FileRemove) error {
	if rm.Path == "" {
		return nil
	}
	if err := os.Remove(rm.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// buildRecord composes a manifest.Record from the install context. ID
// is host:kind:name, suitable as a uniqueness key within one host.
//
// TargetDir comes from plan.TargetDir, NOT a filepath.Dir(plan.Files[0])
// heuristic. The heuristic was skill-shaped: for nested layouts
// (<root>/skills/<name>/SKILL.md) it correctly named the owned dir, but
// for flat layouts (<root>/agents/<name>.md) it pointed at the SHARED
// agents/ dir — uninstall would then try to reclaim a directory holding
// sibling agents and user-authored content. Adapters now declare
// TargetDir explicitly (empty = none owned), per the type docstring on
// adapter.InstallPlan.TargetDir.
//
// Returns an error when the Resource has no ID-derivation path wired up
// (see resourceName). The caller is Install, which propagates so the
// CLI prints a useful message instead of a Go runtime panic backtrace.
func buildRecord(host string, r resource.Resource, scope adapter.Scope, plan adapter.InstallPlan, opts InstallOptions, when time.Time) (manifest.Record, error) {
	name, err := resourceName(r)
	if err != nil {
		return manifest.Record{}, err
	}
	files := make([]string, 0, len(plan.Files))
	for _, f := range plan.Files {
		files = append(files, f.Path)
	}
	sort.Strings(files)

	mks := make([]manifest.MergedKey, 0, len(plan.MergedKeys))
	for _, mk := range plan.MergedKeys {
		entry := manifest.MergedKey{
			File: mk.File,
			Path: mk.Path,
			Op:   string(mk.Op),
		}
		// For Op=Append the manifest needs a content-hash Selector so
		// uninstall can identify the install's array element by hash
		// rather than by numeric index (which is unstable across
		// sibling installs and user reorders per advisor). The hash is
		// computed once at install time and re-derived at uninstall by
		// walking the array — see selectorFor.
		if mk.Op == adapter.MergedKeyAppend {
			selector, err := selectorFor(mk.Value)
			if err != nil {
				return manifest.Record{}, fmt.Errorf("compute selector for %s#%s: %w", mk.File, mk.Path, err)
			}
			entry.Selector = selector
		}
		mks = append(mks, entry)
	}
	sort.Slice(mks, func(i, j int) bool {
		if mks[i].File != mks[j].File {
			return mks[i].File < mks[j].File
		}
		if mks[i].Path != mks[j].Path {
			return mks[i].Path < mks[j].Path
		}
		// Multiple Op=Append entries can share (File, Path) — one per
		// binding in a multi-binding hook resource. Selector
		// disambiguates so the slot order on disk stays deterministic.
		return mks[i].Selector < mks[j].Selector
	})

	return manifest.Record{
		ID:          fmt.Sprintf("%s:%s:%s", host, r.Kind(), name),
		Source:      opts.Source,
		Kind:        string(r.Kind()),
		Agent:       host,
		Scope:       string(scope),
		TargetDir:   plan.TargetDir,
		Files:       files,
		MergedKeys:  mks,
		CacheKey:    cacheKey(plan),
		InstalledAt: when.Format(time.RFC3339),
	}, nil
}

// resourceName extracts the human-readable name field from a Resource
// via the resource.Named interface. Returns an error (not panic) when
// the resource does not implement Named so the CLI can surface a
// useful message rather than a Go stack trace.
//
// Kinds whose identity is NOT a name field (memory by filename + scope,
// mcp-server by JSON-object key) will need an alternate ID-derivation
// path in this function when they land — composing the ID from their
// own typed fields rather than asserting Named.
func resourceName(r resource.Resource) (string, error) {
	n, ok := r.(resource.Named)
	if !ok {
		return "", fmt.Errorf("orchestrator: kind %q has no ID-derivation path; "+
			"the resource type does not implement resource.Named and resourceName "+
			"has no alternate branch wired up for this kind", r.Kind())
	}
	return n.ResourceName(), nil
}

// cacheKey hashes the install plan's contents to give the manifest a
// deduplication / drift-detection handle. Per ADR-0008 the manifest
// carries a cache_key field; in slice 1 the cache layer doesn't yet
// exist, so we synthesise the key from the bytes the adapter emitted.
//
// For config-fragment installs the plan.Files slice is empty, so the
// hash also folds in plan.MergedKeys (file, path, json-marshalled
// value).
//
// Determinism caveat (hostile-review #5): json.Marshal sorts map keys
// lexicographically for map[string]any / map[string]string — that's
// the case for every emit function today (claudecode.emitMCPServer
// returns a map[string]any). Typed Go structs marshal in
// field-declaration order, not alphabetical, so if a future emit
// returns a struct value the cache_key remains stable per-binary but
// may change across struct-field reorderings. The guard is the
// convention "emit functions return map[string]any" — if a future
// emit returns something else, this hash function gains a stricter
// canonicalisation step or a runtime type-check.
func cacheKey(plan adapter.InstallPlan) string {
	h := sha256.New()
	for _, f := range plan.Files {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write(f.Content)
		h.Write([]byte{0})
	}
	for _, mk := range plan.MergedKeys {
		h.Write([]byte(mk.File))
		h.Write([]byte{0})
		h.Write([]byte(mk.Path))
		h.Write([]byte{0})
		valBytes, _ := json.Marshal(mk.Value)
		h.Write(valBytes)
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
