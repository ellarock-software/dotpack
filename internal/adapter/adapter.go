// Package adapter declares the Adapter interface and plan types per
// ADR-0016 §2. Each host (claude-code, gemini, codex, ...) lives in
// its own sub-package and implements Adapter. The orchestrator owns
// Apply: adapters are pure functions of the resource that return an
// InstallPlan; they never touch the filesystem themselves.
package adapter

import (
	"io/fs"

	"github.com/ellarock/dotpack/internal/resource"
)

// Scope is install-time scope: user (~/.claude/skills/<name>/) or
// project (./.claude/skills/<name>/). Per the prior handoff the
// universal model treats this as the install location selector;
// per-kind defaults (e.g. memory's import-resolution differences,
// per ADR-0012) are kind-specific concerns above this.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// CapabilityLevel encodes the per-(kind, adapter) capability matrix
// from ADR-0007.
type CapabilityLevel int

const (
	// Unsupported: adapter refuses installs of this kind with a clear
	// error. No Plan call.
	Unsupported CapabilityLevel = iota
	// Lossy: adapter can install this kind but the universal-core
	// fields don't map cleanly. Requires --allow-lossy at the
	// orchestrator level.
	Lossy
	// Native: adapter installs this kind without loss.
	Native
)

// KindCapabilityMatrix is one Adapter's per-kind rating, returned by
// Adapter.Capabilities().
type KindCapabilityMatrix map[resource.Kind]CapabilityLevel

// FileWrite represents one file the adapter wants the orchestrator to
// write. Per ADR-0008, the content is byte-identical to the cache
// copy for drop-file kinds (no frontmatter mutation).
type FileWrite struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

// MergedKeyWrite represents one key (or array element) the adapter
// wants merged into a host config file. Path is a JSONPath or
// TOMLPath depending on the target file's format. Populates the
// manifest's merged_keys list per ADR-0008.
type MergedKeyWrite struct {
	File string
	Path string
	Value any
}

// LossyReason is one field the adapter would drop on install. Surfaced
// to the user via --allow-lossy. Populated by the orchestrator's
// per-instance lossy detection (ADR-0016 §8) — adapters do not
// populate it; the schema is the single source of truth.
type LossyReason struct {
	FieldPath        string
	CanonicalConcept string
	SupportedHosts   []string
}

// InstallPlan is what Adapter.Plan returns. Apply is the orchestrator's
// job; the plan is data, not behaviour. The plan does NOT carry lossy
// state — per ADR-0016 §8 the orchestrator computes that from the
// schema after Plan returns, against the resource's Extensions and the
// adapter's HostID. Keeping a plan.Lossy field invited adapters to
// restate schema knowledge in code, which §8 explicitly supersedes.
type InstallPlan struct {
	Files      []FileWrite
	MergedKeys []MergedKeyWrite
}

// Adapter is the host-side abstraction per ADR-0016 §2. Implementations
// live in sub-packages (internal/adapter/claudecode, etc.).
type Adapter interface {
	HostID() string
	Capabilities() KindCapabilityMatrix
	Plan(r resource.Resource, scope Scope) (InstallPlan, error)
}
