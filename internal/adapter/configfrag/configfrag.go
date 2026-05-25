// Package configfrag is the deep Adapter implementation for hosts whose
// kinds install as config fragments — merges into an existing host
// config file rather than dropping a new file. Hook + mcp-server are
// config-fragment kinds per ADR-0016 §5–§7; their installs return
// InstallPlan.MergedKeys (not Files), which the orchestrator applies
// via format-aware read-modify-write on the target file.
//
// Why a sibling module to internal/adapter/filedrop:
//
//   - Per-host shells (claudecode, gemini, codex) deal with BOTH shapes
//     once mcp-server lands. Splitting filedrop / configfrag keeps each
//     module focused on one apply contract: filedrop returns
//     InstallPlan.Files; configfrag returns InstallPlan.MergedKeys. The
//     per-host shell dispatches by Kind, delegating to whichever module
//     owns that kind's apply contract.
//   - filedrop's package docstring already pre-positioned this:
//     "The hook + mcp-server kinds (per ADR-0016 §5–§7) are NOT file-drop
//     — they merge config fragments into existing JSON / TOML files.
//     Those will live in a sibling adapter module when they land, not
//     here."
//   - Inline-first / refactor-on-second-host was filedrop's history
//     (architecture review card #1). The advisor explicitly called out
//     that pattern as a refactor we're avoiding by knowing the shape
//     up front.
//
// Format ownership: the adapter emits paths in the file's native syntax
// (JSONPath-ish "$.mcpServers.github" for JSON; dotted "mcp_servers.
// github" for TOML when codex lands). The orchestrator infers format
// from the file extension at apply / un-merge time, so a path-shape vs
// file-extension mismatch surfaces at the format-walker layer, not
// silently.
//
// Per-host divergence (key naming, field rewrites) is handled inside
// each kind's emit function, parameterised by Policy. The Policy carries
// only per-host data; the apply algorithm is shared.
package configfrag

import (
	"fmt"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

// Format identifies the host config file's on-disk encoding. Used by
// the orchestrator's apply / un-merge step to pick the format-aware
// walker (encoding/json today; pelletier/go-toml/v2 when codex lands).
// Adapters declare this at policy-declaration time; orchestrator does
// not need to read the file to detect format.
type Format string

const (
	// FormatJSON: encoding/json read-modify-write. Used by claude (.mcp.json,
	// .claude/settings.json) and gemini (.gemini/settings.json).
	FormatJSON Format = "json"
	// FormatTOML: pelletier/go-toml/v2 read-modify-write. Used by codex
	// (~/.codex/config.toml). NOT yet implemented in the orchestrator's
	// apply step — adding the dep is the codex-slice's first wave.
	FormatTOML Format = "toml"
)

// ScopeFiles resolves the per-scope absolute path for one kind's
// config-fragment target.
//
//   - User: returns the user-scope file (e.g., ~/.claude.json for
//     claude-code mcp-server). May be nil if the host does not document
//     a user-scope path for the kind (the Plan returns a structured
//     "scope not supported" error in that case).
//   - Project: returns the project-scope file (e.g., <ProjectHome>/.mcp.json).
//     May be nil under the same convention.
//
// Resolvers receive the host's dirs and return the absolute path plus
// any host-specific "missing dirs config" error (matching filedrop's
// pattern). Absent scopes are nil so the Plan can return a structured
// scope-not-supported error rather than panicking on a nil call.
type ScopeFiles struct {
	User    func(dirs.Dirs) (string, error)
	Project func(dirs.Dirs) (string, error)
}

// KindConfig declares per-(host, kind) how to install one config-fragment
// resource. The Format + ScopeFiles is per-host policy; the Emit
// callback is the host-specific serializer that turns the typed resource
// into the (path, value) pair the orchestrator applies.
//
// Emit signature: returns the list of (path, value) tuples this resource
// merges. For mcp-server today that's exactly one tuple; for hook it
// will be one tuple per binding leaf path (per ADR-0016 §9: "Multiple
// keys per hook install (one per binding leaf path)") so the slice-of-
// tuples shape is the right surface from day 1 even though the tracer
// bullet uses a single tuple.
type KindConfig struct {
	Format Format
	Files  ScopeFiles
	Emit   func(r resource.Resource) ([]MergedFragment, error)
}

// MergedFragment is one (path, value) tuple the adapter emits at Plan
// time. The orchestrator turns this into an adapter.MergedKeyWrite
// (adding the resolved File path from KindConfig.Files) before applying.
//
// Path is in the file's native syntax for KindConfig.Format — adapters
// emit in the format the file uses, the orchestrator's walker consumes
// in that same format. Per ADR-0016 §5 the path keys are
// schema-declared (see schema/<kind>.yaml's template.source_locations).
type MergedFragment struct {
	Path  string
	Value any
}

// Policy is the per-host data the deep configfrag module dispatches on.
// HostID matches the schema-side host alias. Kinds is the kind →
// KindConfig map; absent kinds are unsupported (the codex-no-hook case
// when it lands, generalised — map membership replaces a separate
// "supports?" flag, matching filedrop's Layouts convention).
type Policy struct {
	HostID string
	Kinds  map[resource.Kind]KindConfig
}

// Adapter is the config-fragment host adapter. Construct via New(d,
// policy); the policy is the only per-host data and is injected at
// construction so the same deep module can satisfy claudecode, gemini,
// and codex.
type Adapter struct {
	dirs   dirs.Dirs
	policy Policy
}

// New constructs the config-fragment adapter for the given dirs +
// policy. Panics on policy misconfiguration (empty HostID, KindConfig
// with nil Emit, KindConfig with both Files scopes nil) — programmer
// bugs that would otherwise surface as inscrutable runtime errors on
// the user's first install.
func New(d dirs.Dirs, p Policy) *Adapter {
	if p.HostID == "" {
		panic("configfrag: Policy.HostID is required")
	}
	for k, cfg := range p.Kinds {
		if cfg.Emit == nil {
			panic(fmt.Sprintf("configfrag: %s: Kinds[%q].Emit is nil — every KindConfig must define an emit function", p.HostID, k))
		}
		if cfg.Files.User == nil && cfg.Files.Project == nil {
			panic(fmt.Sprintf("configfrag: %s: Kinds[%q].Files has no scope resolvers — every KindConfig must support at least one of user/project scope", p.HostID, k))
		}
		switch cfg.Format {
		case FormatJSON, FormatTOML:
		default:
			panic(fmt.Sprintf("configfrag: %s: Kinds[%q].Format = %q is unknown (use FormatJSON or FormatTOML)", p.HostID, k, cfg.Format))
		}
	}
	return &Adapter{dirs: d, policy: p}
}

// HostID returns the policy's host ID — the schema-side alias adapters
// are keyed by.
func (a *Adapter) HostID() string { return a.policy.HostID }

// Plan returns the install plan for a config-fragment resource. Dispatch
// is data-driven via policy.Kinds membership (filedrop's pattern).
// Absent kinds return a structured "kind X not yet supported" error;
// absent scope-resolvers (e.g., a kind that only has project scope) on
// the caller's chosen scope return a structured "scope not supported"
// error — the configfrag adapter does NOT silently fall back to the
// other scope.
//
// Returns InstallPlan with MergedKeys populated and Files empty. The
// orchestrator's apply step reads MergedKeys and runs the format-aware
// read-modify-write; manifest persistence stores (File, Path) tuples
// for uninstall's un-merge step.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	cfg, ok := a.policy.Kinds[r.Kind()]
	if !ok {
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", a.policy.HostID, r.Kind())
	}

	file, err := a.resolveFile(cfg, scope)
	if err != nil {
		return adapter.InstallPlan{}, err
	}

	frags, err := cfg.Emit(r)
	if err != nil {
		return adapter.InstallPlan{}, fmt.Errorf("%s: emit %s: %w", a.policy.HostID, r.Kind(), err)
	}
	if len(frags) == 0 {
		return adapter.InstallPlan{}, fmt.Errorf("%s: emit %s returned no fragments — KindConfig.Emit must emit at least one (path, value) per resource", a.policy.HostID, r.Kind())
	}

	mks := make([]adapter.MergedKeyWrite, 0, len(frags))
	for _, frag := range frags {
		mks = append(mks, adapter.MergedKeyWrite{
			File:  file,
			Path:  frag.Path,
			Value: frag.Value,
		})
	}
	return adapter.InstallPlan{MergedKeys: mks}, nil
}

// resolveFile picks the absolute path for cfg's target file in the
// caller's scope. Returns a structured "scope not supported" error when
// the KindConfig's ScopeFiles does not declare a resolver for the
// requested scope. This is distinct from "kind not supported" — the
// host supports the kind, but only on a different scope.
func (a *Adapter) resolveFile(cfg KindConfig, scope adapter.Scope) (string, error) {
	switch scope {
	case adapter.ScopeUser:
		if cfg.Files.User == nil {
			return "", fmt.Errorf("%s: scope %q not supported for this kind (only project scope is wired)", a.policy.HostID, scope)
		}
		return cfg.Files.User(a.dirs)
	case adapter.ScopeProject:
		if cfg.Files.Project == nil {
			return "", fmt.Errorf("%s: scope %q not supported for this kind (only user scope is wired)", a.policy.HostID, scope)
		}
		return cfg.Files.Project(a.dirs)
	default:
		return "", fmt.Errorf("%s: unknown scope %q", a.policy.HostID, scope)
	}
}
