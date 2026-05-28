// UmbrellaInstaller — orchestrator-side fan-out for CLI flags that
// resolve to an adapter set per ADR-0012 §1+§10. Sibling to Installer;
// see orchestrator.go's package docstring for the (Reader, Installer,
// UmbrellaInstaller) split rationale.
//
// Why this exists as a separate type rather than a method on Installer:
// Installer is the (host, manifest) pair — one adapter, one HostID,
// HostID() drives both lossy-check and record-derivation. An umbrella
// install has NO single host: it aggregates lossy across multiple
// sub-adapters (per ADR-0012 §8's fan-out aggregation) AND it labels
// the manifest record with the umbrella's CLI-flag name ("agents-cli"),
// NOT a sub-adapter's HostID. Splatting both knobs onto Installer would
// require an "AgentLabel override" on InstallOptions and a sub-adapter
// slice the per-host install path would ignore — actively misleading.
// The split keeps each type honest about what it does: Installer is a
// per-host install; UmbrellaInstaller is a CLI-flag-driven fan-out.
//
// Card #3's docstring on Installer ("one Installer handles one (host,
// manifest) pair; --agent agents-cli fan-out is a higher layer") is
// kept literal by this split — UmbrellaInstaller IS that higher layer.
//
// For file-drop convergence, the "fan-out" is actually write-once:
// gemini-cli and codex both honour ~/.agents/skills/, so the umbrella
// picks codex (the documented canonical writer per
// developers.openai.com/codex/skills) to produce the InstallPlan, and
// the single SKILL.md written there is read by both runtimes. ADR-0012
// §1: "for file-drop kinds the orchestrator special-cases the flag to
// write `.agents/` once rather than invoking both sub-adapters with
// redundant writes". Config-fragment kinds are different: each
// sub-adapter writes its own host config file, and this type aggregates
// those MergedKeys into one umbrella manifest record. The per-kind
// writer list lives in the CLI layer's umbrellaFactories (see
// internal/cli/install.go) so this type is umbrella-agnostic — a future
// "all" or "cursor-ish" umbrella plugs in by declaring its own
// (subAdapters, writers, label) triple.
package orchestrator

import (
	"fmt"
	"sort"
	"time"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/manifest"
	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
)

// UmbrellaInstaller installs one resource under a CLI-flag-driven
// adapter set. SubAdapters is the full set the umbrella fans out to for
// lossy aggregation (ADR-0012 §8); Writers picks which sub-adapter
// supplies the InstallPlan for each kind (the write-once convergence
// choice for file-drop). Label is the user-visible umbrella name (e.g.
// "agents-cli") and becomes the manifest record's Agent field and the
// host segment of the ID.
//
// Construct via NewUmbrellaInstaller. The constructor validates that
// every Writer adapter is also a member of SubAdapters — a Writer that's
// not in the lossy-aggregation set would emit fields whose drop on OTHER
// sub-adapters is unchecked, which is the silent-lossy failure mode
// dotpack exists to prevent.
type UmbrellaInstaller struct {
	dirs        dirs.Dirs
	label       string
	subAdapters []adapter.Adapter
	writers     map[resource.Kind][]adapter.Adapter
	manifest    *manifest.Store
	now         func() time.Time
}

// NewUmbrellaInstaller wires the umbrella with its sub-adapter set and
// per-kind writer lists.
//
//   - subAdapters MUST contain every adapter the umbrella resolves to.
//     Lossy aggregation iterates this set; a missing sub-adapter
//     silently bypasses its lossy check (the failure-mode-safety
//     argument in ADR-0012 §Why).
//   - writers MUST cover every kind the umbrella supports. A kind absent
//     from writers means "umbrella does not support this kind" — Install
//     returns a structured error rather than picking a default
//     sub-adapter (which would silently install to one CLI's path while
//     the user typed an umbrella expecting both).
//   - Writer-must-be-in-subAdapters: the CLI's umbrellaFactories init()
//     panics at process start if any writer's HostID isn't in subs.
//     Double-checking it here would be belt-and-suspenders — by the
//     time NewUmbrellaInstaller runs, init has already validated. This
//     constructor trusts that contract.
func NewUmbrellaInstaller(d dirs.Dirs, label string, subAdapters []adapter.Adapter, writers map[resource.Kind][]adapter.Adapter, m *manifest.Store) *UmbrellaInstaller {
	return &UmbrellaInstaller{
		dirs:        d,
		label:       label,
		subAdapters: subAdapters,
		writers:     writers,
		manifest:    m,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// Install runs the umbrella install for one resource. The flow:
//
//  1. Resolve the writer list for the kind. Absent → structured
//     "kind not supported under umbrella" error.
//  2. Aggregate the writers' InstallPlans. Errors propagate (each
//     writer's Plan enforces per-host kind support).
//  3. Aggregate per-instance lossy reasons across ALL sub-adapters per
//     ADR-0012 §8 fan-out semantics. A LossyReason is recorded if ANY
//     sub-adapter would drop the field whose concept is
//     lossy_when_dropped: true. Reasons are deduped by FieldPath so
//     "claude_skill_runtime_overrides not supported on EITHER sub-adapter"
//     surfaces once, not per-sub. If unioned reasons exist and
//     opts.AllowLossy is false, return LossyError naming the umbrella
//     (not a sub-adapter) — the user's CLI-flag identity is what they
//     should see in the failure message.
//  4. Build the manifest record using the umbrella label for Agent and
//     the ID's host segment. Per ADR-0012 §1 + this session's design
//     decision (Option A), the user-typed umbrella IS the record's
//     identity; sub-adapter HostIDs do not surface on the record.
//  5. Pre-flight collision check (shared with per-host Installer) — if
//     a writer target path exists on disk under a DIFFERENT manifest
//     ID, refuse with CollisionError + --force hint. Same protection as
//     Installer: the umbrella's identity (option A) means an existing
//     --agent codex install of the same skill collides with a fresh
//     --agent agents-cli install — the user must explicitly resolve.
//  6. Apply file writes (re-uses writeAtomic from orchestrator.go) and
//     persist the record.
//
// File-drop kinds still use a single canonical writer so the umbrella
// does not generate redundant file writes. Config-fragment kinds use
// multiple writers so each host gets its native config file touched and
// the manifest records every merged key in one install row.
func (u *UmbrellaInstaller) Install(r resource.Resource, scope adapter.Scope, opts InstallOptions) (InstallResult, error) {
	writers, ok := u.writers[r.Kind()]
	if !ok || len(writers) == 0 {
		return InstallResult{}, fmt.Errorf("%s: kind %q not supported under umbrella (no documented cross-host convergence path; add a writer to umbrellaFactories when one is documented)", u.label, r.Kind())
	}

	plan, err := u.aggregatePlans(r, scope, writers)
	if err != nil {
		return InstallResult{}, err
	}

	reasons, err := u.aggregateLossy(r)
	if err != nil {
		return InstallResult{}, fmt.Errorf("lossy check: %w", err)
	}
	if len(reasons) > 0 && !opts.AllowLossy {
		return InstallResult{}, &LossyError{Host: u.label, Reasons: reasons}
	}

	rec, err := buildRecord(u.label, r, scope, plan, opts, u.now())
	if err != nil {
		return InstallResult{}, err
	}

	if !opts.Force {
		collisions, err := preflightCollisions(u.manifest, rec.ID, plan.Files)
		if err != nil {
			return InstallResult{}, fmt.Errorf("collision check: %w", err)
		}
		if len(collisions) > 0 {
			return InstallResult{}, &CollisionError{Paths: collisions}
		}
		mkCollisions, err := preflightMergedKeyCollisions(u.manifest, rec.ID, plan.MergedKeys)
		if err != nil {
			return InstallResult{}, fmt.Errorf("merged-key collision check: %w", err)
		}
		if len(mkCollisions) > 0 {
			return InstallResult{}, &CollisionError{Paths: mkCollisions}
		}
	}

	if err := unmergeExistingAppendsForID(u.manifest, rec.ID); err != nil {
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

	if err := u.manifest.Upsert(rec); err != nil {
		return InstallResult{}, fmt.Errorf("upsert manifest: %w", err)
	}

	return InstallResult{Plan: plan, Record: rec, LossyReasons: reasons}, nil
}

func (u *UmbrellaInstaller) aggregatePlans(r resource.Resource, scope adapter.Scope, writers []adapter.Adapter) (adapter.InstallPlan, error) {
	var out adapter.InstallPlan
	for _, writer := range writers {
		plan, err := writer.Plan(r, scope)
		if err != nil {
			return adapter.InstallPlan{}, fmt.Errorf("plan (%s sub-adapter %s): %w", u.label, writer.HostID(), err)
		}
		out.Files = append(out.Files, plan.Files...)
		out.RemoveFiles = append(out.RemoveFiles, plan.RemoveFiles...)
		out.MergedKeys = append(out.MergedKeys, plan.MergedKeys...)
		if plan.TargetDir == "" {
			continue
		}
		if out.TargetDir == "" {
			out.TargetDir = plan.TargetDir
			continue
		}
		if out.TargetDir != plan.TargetDir {
			return adapter.InstallPlan{}, fmt.Errorf("plan (%s sub-adapter %s): multiple target dirs for umbrella install (%s and %s)", u.label, writer.HostID(), out.TargetDir, plan.TargetDir)
		}
	}
	return out, nil
}

// aggregateLossy walks every sub-adapter and unions their LossyReasons
// per ADR-0012 §8 fan-out semantics (a field is lossy on the umbrella
// if ANY sub-adapter would drop it). Dedup is by FieldPath so a field
// lossy on multiple sub-adapters surfaces once with a stable supporting-
// hosts list — the user reads one reason per field, not per sub-adapter.
//
// Stable iteration order: sub-adapters are walked in their construction
// order (preserved from the umbrellaFactories slice); within one sub,
// schema.LossyExtensions returns sorted FieldPaths. The dedup keeps the
// FIRST sub-adapter's reason for each FieldPath, so the SupportingHosts
// list reflects whichever sub raised the issue first — but since the
// SupportingHosts is a function of the schema concept (not the sub
// raising the reason), the field is deterministic regardless of which
// sub "wins" dedup.
//
// Empirical note (2026-05-25): every deliberately_excluded concept in
// schema/skill.yaml is either claude-code-only (lossy on BOTH sub-
// adapters) or has empty aliases with lossy_when_dropped: false (never
// lossy). For skill kind today, this aggregation produces results
// identical to a single-sub-adapter LossyExtensions call. The algorithm
// is general so a future gemini-XOR-codex extension surfaces correctly
// without re-architecting; it is not over-engineered relative to today's
// schema.
func (u *UmbrellaInstaller) aggregateLossy(r resource.Resource) ([]adapter.LossyReason, error) {
	if len(r.Extensions()) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []adapter.LossyReason
	for _, sub := range u.subAdapters {
		reasons, err := schema.LossyExtensions(r.Kind(), sub.HostID(), r.Extensions())
		if err != nil {
			return nil, fmt.Errorf("%s sub-adapter %s: %w", u.label, sub.HostID(), err)
		}
		for _, reason := range reasons {
			if seen[reason.FieldPath] {
				continue
			}
			seen[reason.FieldPath] = true
			out = append(out, reason)
		}
	}
	// Sort for deterministic output even when sub-adapter ordering or
	// schema.LossyExtensions's internal sort change. The LossyError
	// formatter prints reasons in slice order; users seeing stable
	// output across runs makes regressions diffable.
	sort.Slice(out, func(i, j int) bool { return out[i].FieldPath < out[j].FieldPath })
	return out, nil
}

// resolveAgentsHomeWriter and related per-umbrella helpers are NOT in
// this file — they live in internal/cli/install.go's umbrellaFactories
// alongside the per-host adapterFactories. Keeping them at the CLI layer
// matches Card #4's registry pattern and ADR-0012 §10's "alias table" at
// the orchestrator/CLI boundary. This file is concerned with the install
// algorithm only.
//
// Forward-declared helpers used here but defined in orchestrator.go:
//   - buildRecord(host, r, scope, plan, opts, when) → manifest.Record
//   - preflightCollisions(store, id, files) → []string
//   - writeAtomic(fw) error
//   - InstallOptions, InstallResult, LossyError, CollisionError
//
// They are deliberately not duplicated. UmbrellaInstaller and Installer
// share these helpers because the manifest-record contract (ID =
// agent:kind:name, atomic file writes, collision protection) is the same
// regardless of whether the agent identity comes from a per-host adapter
// or an umbrella label. The label override is the only divergence, and
// it's handled by the host argument to buildRecord (label for umbrella;
// adapter.HostID() for per-host).
//
// Misuse note: do NOT pass a sub-adapter's HostID as buildRecord's host
// argument from this type. The umbrella's design contract (Option A) is
// that the manifest record's identity matches the user-typed --agent
// flag. Records prefixed with a sub-adapter HostID would lose the
// umbrella identity and break the misroute-hint surface + the `dotpack
// list` UX. The line "rec, err := buildRecord(u.label, ...)" is the
// load-bearing one; protect it from future "let's use the writer's
// HostID for consistency with Installer" edits.
