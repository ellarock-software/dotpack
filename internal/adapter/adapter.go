// Package adapter declares the Adapter interface and plan types per
// ADR-0016 §2. Each host (claude-code, gemini-cli, codex, ...) lives
// in its own sub-package and implements Adapter; the HostID strings
// match the `host:` aliases in schema/*.yaml (see schema.Alias). The
// orchestrator owns Apply: adapters are pure functions of the
// resource that return an InstallPlan; they never touch the
// filesystem themselves.
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
//
// TargetDir is the directory the orchestrator may reclaim with a
// single os.Remove on uninstall once empty. Adapters MUST leave it
// empty for kinds whose files live in a shared dir (agents/, hooks/,
// etc.) where dotpack does not own the directory. Skill installs set
// it to <root>/skills/<name>/; agent installs leave it empty because
// <root>/agents/ holds sibling agents and possibly user-authored
// content. Persisted into manifest.Record.TargetDir for the symmetric
// uninstall behaviour.
type InstallPlan struct {
	Files      []FileWrite
	MergedKeys []MergedKeyWrite
	TargetDir  string
}

// Adapter is the host-side abstraction per ADR-0016 §2. Implementations
// live in sub-packages (internal/adapter/claudecode, etc.). Per-kind
// support is expressed by Plan's behaviour: unsupported kinds return a
// typed error ("kind X not yet supported"); supported kinds return an
// InstallPlan. Per-instance lossiness is computed by the orchestrator
// from schema aliases (ADR-0016 §8), not by the adapter.
type Adapter interface {
	HostID() string
	Plan(r resource.Resource, scope Scope) (InstallPlan, error)
}
