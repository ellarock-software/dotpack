// Package claudecode is the claude-code per-host shell. It dispatches
// per-kind to the deep adapter modules:
//
//   - skill + agent → internal/adapter/filedrop (standalone files; no
//     config merging; skills can include packaged support files).
//   - mcp-server + hook → internal/adapter/configfrag (one or more
//     (path, value) merges into a host config file; ADR-0012 §5–§7).
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
// Rule paths: <ClaudeHome>/rules/<name>.md (user) or
// <ProjectHome>/.claude/rules/<name>.md (project).
//
// MCP-server paths: <ProjectHome>/.mcp.json (project) or
// <HomeDir>/.claude.json (user — sibling to ~/.claude/ per
// schema/mcp-server.yaml).
//
// Hook paths: <ProjectHome>/.claude/settings.json (project) or
// <ClaudeHome>/settings.json (user — settings.json IS inside
// ClaudeHome, unlike mcp-server's ~/.claude.json sibling, so user
// scope is wired from day 1). Hook installs append into $.hooks.<Event>
// arrays per ADR-0012 §9; identity at uninstall time is a sha256
// content-hash Selector persisted in manifest.MergedKey (numeric
// indices are unstable when siblings come and go).
package claudecode

import (
	"fmt"
	"path/filepath"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/configfrag"
	"github.com/ellarock-software/dotpack/internal/adapter/filedrop"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
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
// machinery — agents-cli umbrella (ADR-0012 §1), batch capability
// queries — can read it without importing through New(d). Mcp-server
// and hook live in configfragPolicy(); they're config-fragment
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
		resource.KindRule: {
			UserRoot:      userRoot,
			ProjectSubdir: ".claude",
			KindDir:       "rules",
			Nested:        false,
		},
		resource.KindCommand: {
			UserRoot:      userRoot,
			ProjectSubdir: ".claude",
			KindDir:       "commands",
			Nested:        false,
		},
		resource.KindMemory: {
			UserRoot:      userRoot,
			ProjectSubdir: "",
			KindDir:       "",
			Nested:        false,
			PreserveName:  true,
		},
	},
	AgentToolsShape: filedrop.ToolsCommaString,
}

// userMCPFile returns <HomeDir>/.claude.json — claude-code's user-scope
// target for mcp-server installs per schema/mcp-server.yaml. It uses
// dirs.HomeDir rather than deriving a parent from ClaudeHome because
// ClaudeHome may be an arbitrary test temp dir, not necessarily
// <HomeDir>/.claude.
func userMCPFile(d dirs.Dirs) (string, error) {
	if d.HomeDir == "" {
		return "", fmt.Errorf("claude-code: user scope requires dirs.HomeDir to be set")
	}
	return filepath.Join(d.HomeDir, ".claude.json"), nil
}

// projectMCPFile returns <ProjectHome>/.mcp.json — claude-code's
// project-scope target for mcp-server installs per schema/mcp-server.yaml's
// template.source_locations entry for host claude-code.
func projectMCPFile(d dirs.Dirs) (string, error) {
	if d.ProjectHome == "" {
		return "", fmt.Errorf("claude-code: project scope requires dirs.ProjectHome to be set")
	}
	return filepath.Join(d.ProjectHome, ".mcp.json"), nil
}

// projectSettingsFile returns <ProjectHome>/.claude/settings.json —
// claude-code's project-scope target for hook installs per
// schema/hook.yaml template.source_locations entry for host claude-code.
func projectSettingsFile(d dirs.Dirs) (string, error) {
	if d.ProjectHome == "" {
		return "", fmt.Errorf("claude-code: project scope requires dirs.ProjectHome to be set")
	}
	return filepath.Join(d.ProjectHome, ".claude", "settings.json"), nil
}

// userSettingsFile returns <ClaudeHome>/settings.json — claude-code's
// user-scope target for hook installs. Unlike mcp-server's ~/.claude.json
// (sibling of ~/.claude/), settings.json IS inside ClaudeHome, so user
// scope rides the existing dirs.ClaudeHome surface from day 1 — no
// HomeDir field gap.
func userSettingsFile(d dirs.Dirs) (string, error) {
	if d.ClaudeHome == "" {
		return "", fmt.Errorf("claude-code: user scope requires dirs.ClaudeHome to be set")
	}
	return filepath.Join(d.ClaudeHome, "settings.json"), nil
}

// emitHook turns a *resource.Hook into one MergedFragment per binding
// across all the resource's events. Op=Append so the orchestrator
// walker appends each binding to the host's $.hooks.<Event> array
// rather than overwriting it; the manifest's sha256 Selector for each
// fragment is computed by the orchestrator at install time and re-
// derived at uninstall to find the right array element regardless of
// sibling installs/reorders.
//
// Per-host divergence (claude-code identity for event names,
// canonical-seconds timeout, no Gemini-only field re-emit) is the
// trivial slice — claude's emit is the canonical shape ADR-0012 §5
// designates. Gemini and Codex each have their own emit function with
// the per-host rewrites
// (Gemini's BeforeTool/AfterTool + ms timeout; Codex identity for
// most events but its TOML walker for the apply step).
//
// Universal-core fields only; Gemini's async/once/name/description
// extensions are either claude-code lossy (caught by orchestrator §8
// aggregation when the umbrella widens) or pure-Gemini-bookkeeping
// (lossy_when_dropped=false per schema/hook.yaml). When a claude-
// specific extension surfaces in the corpus, this function gains a
// HostKeepsExtension filter mirroring filedrop.encodeSkill's pattern.
func emitHook(r resource.Resource) ([]configfrag.MergedFragment, error) {
	h, ok := r.(*resource.Hook)
	if !ok {
		return nil, fmt.Errorf("emit hook: resource type %T is not *resource.Hook", r)
	}
	if len(h.Events) == 0 {
		return nil, fmt.Errorf("emit hook: %s has no events", h.Name)
	}
	frags := make([]configfrag.MergedFragment, 0)
	for _, ev := range h.Events {
		for _, b := range ev.Bindings {
			value := encodeBinding(b)
			frags = append(frags, configfrag.MergedFragment{
				Path:  "$.hooks." + ev.Event,
				Value: value,
				Op:    adapter.MergedKeyAppend,
			})
		}
	}
	return frags, nil
}

// encodeBinding serialises one Binding into the on-disk JSON shape
// claude expects: { matcher, hooks: [...specs...] }. Optional fields
// (matcher, hook-spec timeout/statusMessage/env) are omitted when
// absent so the written bytes match what a hand-author would have
// produced — readability matters for a file the user diffs by hand.
//
// Output uses map[string]any (not a typed struct) so json.Marshal
// sorts keys lexicographically — same convention as
// emitMCPServer/applyJSONMergedKey, which keeps cacheKey's hash
// deterministic per the cacheKey docstring's
// "emit returns map[string]any" guard.
func encodeBinding(b resource.Binding) map[string]any {
	out := map[string]any{}
	if b.Matcher != "" {
		out["matcher"] = b.Matcher
	}
	specs := make([]any, 0, len(b.Hooks))
	for _, s := range b.Hooks {
		specs = append(specs, encodeHookSpec(s))
	}
	out["hooks"] = specs
	return out
}

// encodeHookSpec serialises one HookSpec leaf. type + command always
// present; optional fields suppressed when absent.
func encodeHookSpec(s resource.HookSpec) map[string]any {
	out := map[string]any{
		"type":    s.Type,
		"command": s.Command,
	}
	if s.HasTimeout {
		out["timeout"] = s.Timeout
	}
	if s.StatusMessage != "" {
		out["statusMessage"] = s.StatusMessage
	}
	if len(s.Env) > 0 {
		// Pass through as map[string]string. Go's json.Marshal sorts
		// string-keyed map keys lexicographically regardless of value
		// type, so map[string]string produces byte-identical output
		// to map[string]any for the same string→string mapping. The
		// selectorFor hash stays stable across install (this emit) and
		// uninstall (where json.Unmarshal of the file produces a
		// map[string]any with the same value bytes). Hostile-review #4.
		out["env"] = s.Env
	}
	return out
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
					User:    userMCPFile,
					Project: projectMCPFile,
				},
				Emit: emitMCPServer,
			},
			resource.KindHook: {
				Format: configfrag.FormatJSON,
				Files: configfrag.ScopeFiles{
					Project: projectSettingsFile,
					User:    userSettingsFile,
				},
				Emit: emitHook,
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

// Plan dispatches by Kind. File-drop kinds (skill, agent, rule) go to the
// filedrop adapter; config-fragment kinds (mcp-server today, hook
// later) go to the configfrag adapter. Kinds neither adapter supports
// surface as a structured "not yet supported" error rather than a
// dispatcher-side panic — the per-adapter Plan paths already produce
// that message, so we delegate.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch r.Kind() {
	case resource.KindSkill, resource.KindAgent, resource.KindRule, resource.KindCommand, resource.KindMemory:
		return a.filedrop.Plan(r, scope)
	case resource.KindMCPServer, resource.KindHook:
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
