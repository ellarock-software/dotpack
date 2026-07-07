// Package adapter declares the Adapter interface and plan types per
// ADR-0012 §2. Each host (claude-code, gemini-cli, codex, ...) lives
// in its own sub-package and implements Adapter; the HostID strings
// match the `host:` aliases in schema/*.yaml (see schema.Alias). The
// orchestrator owns Apply: adapters are pure functions of the
// resource that return an InstallPlan; they never touch the
// filesystem themselves.
package adapter

import (
	"io/fs"

	"github.com/ellarock-software/dotpack/internal/resource"
)

// Scope is install-time scope: user (~/.claude/skills/<name>/) or
// project (./.claude/skills/<name>/). Per the prior handoff the
// universal model treats this as the install location selector;
// per-kind defaults (e.g. memory's import-resolution differences,
// per ADR-0008) are kind-specific concerns above this.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// FileWrite represents one file the adapter wants the orchestrator to
// write. Per ADR-0004, the content is byte-identical to the cache
// copy for drop-file kinds (no frontmatter mutation).
type FileWrite struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
}

// FileRemove represents one stale compatibility file the adapter wants
// the orchestrator to remove after successful writes. These are not
// manifest-owned install claims; they are migration cleanup paths such
// as legacy .agents/rules/gemini/<name>.md files superseded by a shared
// .agents/rules/<name>.md rule.
type FileRemove struct {
	Path string
}

// MergedKeyOp distinguishes the two merge semantics the orchestrator's
// walker supports against the parsed path:
//
//   - "" (MergedKeySet, the default): the path leaf is a value to set —
//     the host config has a single named slot the install fills, e.g.,
//     $.mcpServers.github = {...}. Idempotent under re-install.
//   - "append" (MergedKeyAppend): the path target is an ARRAY the
//     install appends to, e.g., $.hooks.PreToolUse += {matcher, hooks}.
//     The manifest records a content-hash Selector at install time so
//     uninstall identifies the install's element by hash rather than
//     by numeric index (which moves when siblings come and go).
//
// The empty string default keeps the prior mcp-server install path
// shape-unchanged when YAML-decoded from manifests written before this
// field existed.
type MergedKeyOp string

const (
	MergedKeySet    MergedKeyOp = ""
	MergedKeyAppend MergedKeyOp = "append"
)

// MergedKeyWrite represents one key (or array element) the adapter
// wants merged into a host config file. Path is a JSONPath or
// TOMLPath depending on the target file's format. Populates the
// manifest's merged_keys list per ADR-0004.
//
// Op selects between set-leaf (mcp-server's $.mcpServers.<name>
// semantics) and append-to-array (hook's $.hooks.<event> semantics
// per ADR-0012 §9). The zero value (MergedKeySet) preserves the
// pre-Op behaviour so existing mcp-server emit functions stay
// shape-unchanged.
type MergedKeyWrite struct {
	File  string
	Path  string
	Value any
	Op    MergedKeyOp
}

// LossyReason is one field the adapter would drop on install. Surfaced
// to the user via --allow-lossy. Populated by the orchestrator's
// per-instance lossy detection (ADR-0012 §8) — adapters do not
// populate it; the schema is the single source of truth.
type LossyReason struct {
	FieldPath        string
	CanonicalConcept string
	SupportedHosts   []string
}

// InstallPlan is what Adapter.Plan returns. Apply is the orchestrator's
// job; the plan is data, not behaviour. The plan does NOT carry lossy
// state — per ADR-0012 §8 the orchestrator computes that from the
// schema after Plan returns, against the resource's Extensions and the
// adapter's HostID. Keeping a plan.Lossy field invited adapters to
// restate schema knowledge in code, which §8 explicitly supersedes.
//
// TargetDir is the directory the orchestrator may reclaim on uninstall
// once empty, including empty support-file subdirectories under it.
// Adapters MUST leave it empty for kinds whose files live in a shared dir
// (agents/, hooks/, etc.) where dotpack does not own the directory. Skill
// installs set it to <root>/skills/<name>/; agent installs leave it empty
// because <root>/agents/ holds sibling agents and possibly user-authored
// content. Persisted into manifest.Record.TargetDir for the symmetric
// uninstall behaviour.
type InstallPlan struct {
	Files       []FileWrite
	RemoveFiles []FileRemove
	MergedKeys  []MergedKeyWrite
	TargetDir   string
}

// Adapter is the host-side abstraction per ADR-0012 §2. Implementations
// live in sub-packages (internal/adapter/claudecode, etc.). Per-kind
// support is expressed by Plan's behaviour: unsupported kinds return a
// typed error ("kind X not yet supported"); supported kinds return an
// InstallPlan. Per-instance lossiness is computed by the orchestrator
// from schema aliases (ADR-0012 §8), not by the adapter.
type Adapter interface {
	HostID() string
	Plan(r resource.Resource, scope Scope) (InstallPlan, error)
}

// KindLayout is the host-visible on-disk shape for one file-drop kind,
// projected from an adapter's internal policy. It is the metadata
// reconcile/scan and CLI help need to walk a host's materialized tree
// WITHOUT hard-coding per-host directory tables (ADR-0014). The fields
// mirror filedrop.Layout's externally-observable subset.
//
//   - Kind is the resource kind this layout describes.
//   - ProjectSubdir is the per-host subdirectory under the project root
//     for project-scope installs (".claude", ".gemini", ...). Empty for
//     kinds that live at the project root (memory).
//   - KindDir is the per-kind directory under the scope root ("skills",
//     "agents", ...). Empty for kinds written directly at the root.
//   - Ext is the flat-file extension (".md", ".toml"). Empty for nested
//     kinds (the file name is NestedFile instead).
//   - Nested switches <dir>/<name>/<NestedFile> (true) vs <dir>/<name><Ext>
//     (false).
//   - NestedFile is the in-subdir filename when Nested is true ("SKILL.md").
type KindLayout struct {
	Kind          resource.Kind
	ProjectSubdir string
	KindDir       string
	Ext           string
	Nested        bool
	NestedFile    string
}

// LayoutDescriber is the OPTIONAL capability an adapter implements to
// expose its file-drop layouts as data (ADR-0014). Reconcile's
// materialized-file scan and the CLI help text iterate the adapter
// registry and query this instead of carrying a hard-coded host→layout
// table. Adapters that do not implement it (or whose kinds are all
// config-fragment) are simply skipped by layout-driven callers. Keeping
// it a separate interface — not a method on Adapter — means a new host
// can ship Plan-only and opt into scan coverage later.
type LayoutDescriber interface {
	// DescribeLayouts returns one KindLayout per file-drop kind this host
	// supports, in a stable order.
	DescribeLayouts() []KindLayout
}
