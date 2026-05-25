// Package claudecode is the claude-code per-host shell. It dispatches
// per-kind to the deep adapter modules:
//
//   - skill + agent → internal/adapter/filedrop (one file written per
//     resource; no config merging).
//   - mcp-server   → internal/adapter/configfrag (one or more
//     (path, value) merges into a host config file; ADR-0016 §5–§7).
//
// Both modules implement adapter.Adapter; the shell's job is to declare
// the per-host policy data (Policy for filedrop; configfragPolicy() for
// configfrag) and dispatch Plan by resource Kind. This split keeps each
// deep module focused on one apply contract (Files vs MergedKeys) and
// matches filedrop's package docstring which pre-positioned the
// configfrag sibling.
//
// Skill paths: <ClaudeHome>/skills/<name>/SKILL.md (user) or
// <ProjectHome>/.claude/skills/<name>/SKILL.md (project).
// Agent paths: <ClaudeHome>/agents/<name>.md (user) or
// <ProjectHome>/.claude/agents/<name>.md (project). Skills nest (own
// the per-name subdir); agents are flat (shared dir, not reclaimed by
// uninstall).
//
// MCP-server paths: <ProjectHome>/.mcp.json (project). User scope
// (~/.claude.json sibling to ~/.claude/) is deferred — dirs.Dirs has no
// HomeDir field today, and the tracer-slice scope is project-only per
// advisor. When user scope lands, configfragPolicy gains a User
// resolver and tests follow.
package claudecode

import (
	"fmt"
	"path/filepath"

	"github.com/ellarock/dotpack/internal/adapter"
	"github.com/ellarock/dotpack/internal/adapter/configfrag"
	"github.com/ellarock/dotpack/internal/adapter/filedrop"
	"github.com/ellarock/dotpack/internal/dirs"
	"github.com/ellarock/dotpack/internal/resource"
)

// hostID is the dotpack adapter HostID for claude-code. MUST match
// the `host:` strings in schema/*.yaml aliases — schema.HostKeepsExtension
// compares on string equality, so a mismatch silently flips claude-code's
// native concepts to lossy on its own adapter.
const hostID = "claude-code"

// userRoot returns ClaudeHome with the host-specific missing-dir error.
// Shared by the skill + agent Layouts so the message format is one
// string in one place.
func userRoot(d dirs.Dirs) (string, error) {
	if d.ClaudeHome == "" {
		return "", fmt.Errorf("claude-code: user scope requires dirs.ClaudeHome to be set")
	}
	return d.ClaudeHome, nil
}

// Policy is the claude-code per-host filedrop policy (skill + agent
// kinds). Exported (not unexported `policy`) so future cross-adapter
// machinery — agents-cli umbrella (ADR-0016 §1), batch capability
// queries — can read it without importing through New(d). Mcp-server
// (and future hook) live in configfragPolicy(); they're config-fragment
// kinds, not file-drop, so they ride a separate policy structure.
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".claude",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
		resource.KindAgent: {
			UserRoot:      userRoot,
			ProjectSubdir: ".claude",
			KindDir:       "agents",
			Nested:        false,
		},
	},
	AgentToolsShape: filedrop.ToolsCommaString,
}

// projectMCPFile returns <ProjectHome>/.mcp.json — claude-code's
// project-scope target for mcp-server installs per schema/mcp-server.yaml's
// template.source_locations entry for host claude-code.
//
// Note: claude has a per-user alternate at ~/.claude.json (NOT inside
// ~/.claude/). User scope is deferred because dirs.Dirs has no HomeDir
// field; resolving "~/.claude.json" from ClaudeHome by going up a
// directory would be fragile in tests (t.TempDir() is not parented by
// a ".claude"-suffixed path). When user scope is wired, add a
// userMCPFile resolver here and a corresponding test.
func projectMCPFile(d dirs.Dirs) (string, error) {
	if d.ProjectHome == "" {
		return "", fmt.Errorf("claude-code: project scope requires dirs.ProjectHome to be set")
	}
	return filepath.Join(d.ProjectHome, ".mcp.json"), nil
}

// emitMCPServer turns a *resource.MCPServer into one (path, value)
// fragment the configfrag adapter merges into .mcp.json. The path is
// JSONPath-ish ($.mcpServers.<name>) — claude's .mcp.json uses
// camelCase mcpServers as the top-level key per
// schema/mcp-server.yaml's source_locations.
//
// Value layout matches claude's documented .mcp.json shape: command,
// args, url, env at the entry level. Universal-core fields only today;
// extension fields (codex's enabled_tools, http_headers, etc.) are NOT
// emitted on claude — they'd be either lossy (caught by orchestrator
// §8 aggregation) or claude-specific (none in the corpus today). When
// a claude-specific extension surfaces, this function gains a
// HostKeepsExtension filter mirroring filedrop.encodeSkill's pattern.
func emitMCPServer(r resource.Resource) ([]configfrag.MergedFragment, error) {
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
	return []configfrag.MergedFragment{{
		Path:  "$.mcpServers." + m.Name,
		Value: value,
	}}, nil
}

// configfragPolicy returns the claude-code configfrag policy. Function
// (not var) because nothing reads it via the package surface today; if
// future cross-adapter introspection needs read access, promote to var.
func configfragPolicy() configfrag.Policy {
	return configfrag.Policy{
		HostID: hostID,
		Kinds: map[resource.Kind]configfrag.KindConfig{
			resource.KindMCPServer: {
				Format: configfrag.FormatJSON,
				Files: configfrag.ScopeFiles{
					Project: projectMCPFile,
					// User: deferred — see projectMCPFile docstring.
				},
				Emit: emitMCPServer,
			},
		},
	}
}

// Adapter is the claude-code per-host shell that dispatches Plan to the
// right deep module by resource Kind. Implements adapter.Adapter; the
// per-host registries in internal/cli (adapterFactories, isBuildableAgent)
// type these as adapter.Adapter so the concrete return type is
// transparent across the boundary.
type Adapter struct {
	filedrop   *filedrop.Adapter
	configfrag *configfrag.Adapter
}

// New constructs the claude-code adapter, wiring the package-level
// Policy with the given Dirs and constructing the configfrag adapter
// from configfragPolicy.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{
		filedrop:   filedrop.New(d, Policy),
		configfrag: configfrag.New(d, configfragPolicy()),
	}
}

// HostID returns the schema-side host alias.
func (a *Adapter) HostID() string { return hostID }

// Plan dispatches by Kind. File-drop kinds (skill, agent) go to the
// filedrop adapter; config-fragment kinds (mcp-server today, hook
// later) go to the configfrag adapter. Kinds neither adapter supports
// surface as a structured "not yet supported" error rather than a
// dispatcher-side panic — the per-adapter Plan paths already produce
// that message, so we delegate.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch r.Kind() {
	case resource.KindSkill, resource.KindAgent:
		return a.filedrop.Plan(r, scope)
	case resource.KindMCPServer:
		return a.configfrag.Plan(r, scope)
	default:
		// Both filedrop and configfrag would also return this shape —
		// being explicit at the dispatcher keeps the error message
		// stable when a kind is unsupported by both modules (the user's
		// "claude-code: kind X not yet supported" is the same regardless
		// of which deep module would have rejected it).
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", hostID, r.Kind())
	}
}
