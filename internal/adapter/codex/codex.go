// Package codex is the codex CLI per-host shell. It dispatches per-kind
// to the deep adapter modules:
//
//   - skill → internal/adapter/filedrop (one file written per resource).
//   - mcp-server → internal/adapter/configfrag (one (path, value) merge
//     into ~/.codex/config.toml; ADR-0016 §5–§7).
//
// Both modules implement adapter.Adapter; the shell's job is to declare
// the per-host policy data (Policy for filedrop; configfragPolicy() for
// configfrag) and dispatch Plan by resource Kind. Mirror of claudecode's
// pattern; the split keeps each deep module focused on one apply
// contract.
//
// Codex supports skill only on the file-drop side — there is no native
// agent loading directory documented by the codex CLI. The absence of
// resource.KindAgent from Policy.Layouts is the canonical declaration
// of that: the filedrop module returns "kind agent not yet supported"
// for any Plan(KindAgent) call. Agent support would be added by
// appending a KindAgent Layout entry + setting AgentToolsShape; that
// requires the codex CLI to document a native agent loading directory
// analogous to .claude/agents/.
//
// Skill paths: <AgentsHome>/skills/<name>/SKILL.md (user) or
// <ProjectHome>/.agents/skills/<name>/SKILL.md (project). Per
// developers.openai.com/codex/skills, AgentsHome is codex's only
// documented native skill root. Gemini CLI ALSO reads ~/.agents/skills/
// as a convergence path, but the gemini-cli adapter writes to its
// host-specific path so `--agent codex` and `--agent gemini-cli` don't
// collide here today. The future `--agent agents-cli` umbrella flag
// (ADR-0016 §1) will write to AgentsHome/skills/ as its write-once
// convergence; collision handling is owned by that umbrella's CLI-flag-
// to-adapter-set special case when it lands.
//
// MCP-server paths: <CodexHome>/config.toml (user — codex spec's
// canonical location per schema/mcp-server.yaml's source_locations).
// Project scope (<ProjectHome>/.codex/config.toml — codex's documented
// alternate) is deferred; the schema notes both paths as valid but the
// user-scope file is the more common pattern in the corpus and matches
// codex's own docs at developers.openai.com/codex/config-reference. The
// configfrag adapter's ScopeFiles.Project remains nil today so a Plan
// with scope=project returns a structured "scope not supported" error;
// project scope wires when a slice has reason to touch ~/.codex/.
package codex

import (
	"fmt"
	"path/filepath"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/configfrag"
	"github.com/ellarock/dotpack/internal/adapter/filedrop"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

// hostID is the dotpack adapter HostID for codex. MUST match the
// `host:` strings in schema/*.yaml aliases — schema.HostKeepsExtension
// compares on string equality.
const hostID = "codex"

// userRoot returns AgentsHome with the host-specific missing-dir error.
// Codex's skill path is AgentsHome (not CodexHome) per the package
// docstring — keep this resolver focused on the skill case so a future
// reader debugging "where does codex write skills?" sees the right env
// var (DOTPACK_AGENTS_HOME).
func userRoot(d dirs.Dirs) (string, error) {
	if d.AgentsHome == "" {
		return "", fmt.Errorf("codex: user scope requires dirs.AgentsHome to be set")
	}
	return d.AgentsHome, nil
}

// userConfigTomlFile returns <CodexHome>/config.toml — codex's
// user-scope target for mcp-server installs per schema/mcp-server.yaml's
// template.source_locations entry for host codex. CodexHome (NOT
// AgentsHome) — see package docstring on why these are distinct env
// vars.
func userConfigTomlFile(d dirs.Dirs) (string, error) {
	if d.CodexHome == "" {
		return "", fmt.Errorf("codex: user scope requires dirs.CodexHome to be set")
	}
	return filepath.Join(d.CodexHome, "config.toml"), nil
}

// Policy is the codex per-host filedrop policy (skill kind only).
// KindAgent is INTENTIONALLY ABSENT from Layouts — codex CLI documents
// no native agent loading directory per developers.openai.com/codex (a
// deliberate decision, not an oversight). filedrop.Plan returns "kind
// agent not yet supported" for any Plan(KindAgent) call as a result.
// AgentToolsShape is intentionally left at zero value (ToolsShapeUnused)
// because no agent Layout exists. Mcp-server (and future hook) live in
// configfragPolicy(); they're config-fragment kinds, not file-drop, so
// they ride a separate policy structure.
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".agents",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
	},
}

// emitMCPServerCodex turns a *resource.MCPServer into one (path, value)
// fragment the configfrag adapter merges into ~/.codex/config.toml. The
// path is TOML-dotted (`mcp_servers.<name>`) — codex's config.toml uses
// snake_case `mcp_servers` as the top-level table key per
// schema/mcp-server.yaml's source_locations.
//
// Value layout: universal core (command, args, url, env) PLUS the
// codex-superset extension fields verbatim from m.Extensions(). Per
// schema/mcp-server.yaml's ecosystem_notes + ADR-0014, codex's
// mcp-server schema is a SUPERSET of the cross-host common core; the
// codex adapter preserves extensions on round-trip (`enabled_tools`,
// `http_headers`, `bearer_token_env_var`, `tools.<id>.approval_mode`,
// etc.). Per-instance lossy detection (ADR-0007 addendum) is a
// non-codex adapter concern; codex emit is non-lossy by definition.
//
// Schema's `headers` vs `http_headers` normalization is INTENTIONALLY
// NOT done here. The schema names canonicalization as a TRANSLATOR
// concern (per ecosystem_notes: "Translator canonicalises on import"),
// not an adapter concern. The codex adapter passes whichever spelling
// arrived; the translator (when it lands) rewrites at the source-import
// boundary so this emit never sees the inconsistency.
//
// Output uses map[string]any so go-toml/v2's Marshal sorts keys
// lexicographically — same convention as emitMCPServer/applyJSONMergedKey,
// which keeps cacheKey's hash deterministic per the cacheKey
// docstring's "emit returns map[string]any" guard. orchestrator.writeTOML
// runs normalizeForTOML on the root before marshal to coerce integral
// float64 (e.g., codex-superset `startup_timeout_sec: 30` arriving from
// a JSON source) → int64, so the user sees `30` not `30.0` in their
// config.toml.
func emitMCPServerCodex(r resource.Resource) ([]configfrag.MergedFragment, error) {
	m, ok := r.(*resource.MCPServer)
	if !ok {
		return nil, fmt.Errorf("emit mcp-server: resource type %T is not *resource.MCPServer", r)
	}
	value := map[string]any{}
	if m.Command != "" {
		value["command"] = m.Command
	}
	if m.Args != nil {
		value["args"] = m.Args
	}
	if m.URL != "" {
		value["url"] = m.URL
	}
	if len(m.Env) > 0 {
		value["env"] = m.Env
	}
	for k, v := range m.Extensions() {
		// Universal-core field names take precedence — extensions
		// must not silently overwrite a typed field. The parser
		// already routes core fields to typed slots, so this branch
		// is defensive (a future ParseMCPServer that adds a new core
		// field whose name overlaps with an old extension key would
		// surface as a duplicate write to value[k] otherwise).
		if _, taken := value[k]; taken {
			continue
		}
		value[k] = v
	}
	return []configfrag.MergedFragment{{
		Path:  "mcp_servers." + m.Name,
		Value: value,
	}}, nil
}

// configfragPolicy returns the codex configfrag policy. Function (not
// var) mirroring claudecode's pattern: nothing reads it via the package
// surface today, and promotion to var would suggest cross-package
// introspection that doesn't exist.
func configfragPolicy() configfrag.Policy {
	return configfrag.Policy{
		HostID: hostID,
		Kinds: map[resource.Kind]configfrag.KindConfig{
			resource.KindMCPServer: {
				Format: configfrag.FormatTOML,
				Files: configfrag.ScopeFiles{
					User: userConfigTomlFile,
					// Project: deferred — see package docstring on
					// <ProjectHome>/.codex/config.toml as a documented
					// alternate codex doesn't promote to canonical.
				},
				Emit: emitMCPServerCodex,
			},
		},
	}
}

// Adapter is the codex per-host shell that dispatches Plan to the right
// deep module by resource Kind. Mirror of claudecode.Adapter; the
// per-host registries in internal/cli (adapterFactories) type these as
// adapter.Adapter so the concrete return type is transparent across
// the boundary.
type Adapter struct {
	filedrop   *filedrop.Adapter
	configfrag *configfrag.Adapter
}

// New constructs the codex adapter, wiring the package-level Policy
// (filedrop) with the given Dirs and constructing the configfrag
// adapter from configfragPolicy.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{
		filedrop:   filedrop.New(d, Policy),
		configfrag: configfrag.New(d, configfragPolicy()),
	}
}

// HostID returns the schema-side host alias.
func (a *Adapter) HostID() string { return hostID }

// Plan dispatches by Kind. File-drop kinds (skill) go to the filedrop
// adapter; config-fragment kinds (mcp-server today, hook when it lands)
// go to the configfrag adapter. Kinds neither adapter supports surface
// as a structured "not yet supported" error — the per-adapter Plan
// paths already produce that message, so we delegate.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch r.Kind() {
	case resource.KindSkill, resource.KindAgent:
		return a.filedrop.Plan(r, scope)
	case resource.KindMCPServer, resource.KindHook:
		return a.configfrag.Plan(r, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", hostID, r.Kind())
	}
}
