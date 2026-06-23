// Package gemini is the gemini-cli per-host shell. It dispatches
// per-kind to the deep adapter modules:
//
//   - skill + agent → internal/adapter/filedrop (standalone files; no
//     config merging; skills can include packaged support files).
//   - mcp-server → internal/adapter/configfrag (one (path, value) merge
//     into Gemini's settings.json; ADR-0012 §5-§7).
//
// Both modules implement adapter.Adapter; this package declares the
// host-specific policy data and dispatches Plan by resource Kind.
// Hook paths: same settings.json target as mcp-server, under $.hooks.
// Gemini rewrites PreToolUse/PostToolUse to BeforeTool/AfterTool and
// emits hook timeouts in milliseconds.
//
// Skill paths: <GeminiHome>/skills/<name>/SKILL.md (user) or
// <ProjectHome>/.gemini/skills/<name>/SKILL.md (project).
// Agent paths: <GeminiHome>/agents/<name>.md (user) or
// <ProjectHome>/.gemini/agents/<name>.md (project). Skills nest;
// agents are flat.
// Rule paths: <GeminiHome>/rules/<name>.md (user) or
// <ProjectHome>/.gemini/rules/<name>.md (project).
// MCP-server paths: <GeminiHome>/settings.json (user) or
// <ProjectHome>/.gemini/settings.json (project), under $.mcpServers.
//
// Convergence note: per schema/skill.yaml ecosystem_notes, gemini-cli
// ALSO reads the shared `~/.agents/skills/` path for the skill kind.
// Codex WRITES to that shared path; gemini-cli deliberately writes to
// its host-specific GeminiHome/skills/ instead so `--agent gemini-cli`
// and `--agent codex` produce distinct manifest-tracked targets. The
// gemini-cli runtime still picks up the codex-installed skill via its
// read-side convergence. The future `--agent agents-cli` umbrella
// flag (ADR-0012 §1) will special-case writing to ~/.agents/skills/
// once for both hosts.
package gemini

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/configfrag"
	"github.com/ellarock-software/dotpack/internal/adapter/filedrop"
	"github.com/ellarock-software/dotpack/internal/adapter/hookcmd"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/schema"
)

// hostID is the dotpack adapter HostID for gemini-cli. MUST match the
// `host:` strings in schema/*.yaml aliases — schema.HostKeepsExtension
// compares on string equality.
const hostID = "gemini-cli"

// userRoot returns GeminiHome with the host-specific missing-dir error.
func userRoot(d dirs.Dirs) (string, error) {
	if d.GeminiHome == "" {
		return "", fmt.Errorf("gemini-cli: user scope requires dirs.GeminiHome to be set")
	}
	return d.GeminiHome, nil
}

// userSettingsFile returns <GeminiHome>/settings.json — gemini-cli's
// user-scope target for mcp-server installs per schema/mcp-server.yaml.
func userSettingsFile(d dirs.Dirs) (string, error) {
	if d.GeminiHome == "" {
		return "", fmt.Errorf("gemini-cli: user scope requires dirs.GeminiHome to be set")
	}
	return filepath.Join(d.GeminiHome, "settings.json"), nil
}

// projectSettingsFile returns <ProjectHome>/.gemini/settings.json —
// gemini-cli's project-scope target for mcp-server installs.
func projectSettingsFile(d dirs.Dirs) (string, error) {
	if d.ProjectHome == "" {
		return "", fmt.Errorf("gemini-cli: project scope requires dirs.ProjectHome to be set")
	}
	return filepath.Join(d.ProjectHome, ".gemini", "settings.json"), nil
}

// Policy is the gemini-cli per-host data the filedrop module dispatches
// on. Tools shape is YAML array (Gemini convention) — the inverse of
// claudecode's comma-string coercion.
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".gemini",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
		resource.KindAgent: {
			UserRoot:      userRoot,
			ProjectSubdir: ".gemini",
			KindDir:       "agents",
			Nested:        false,
		},
		resource.KindRule: {
			UserRoot:      userRoot,
			ProjectSubdir: ".gemini",
			KindDir:       "rules",
			Nested:        false,
		},
		resource.KindCommand: {
			UserRoot:      userRoot,
			ProjectSubdir: ".gemini",
			KindDir:       "commands",
			Nested:        false,
			FlatExt:       ".toml",
		},
		resource.KindMemory: {
			UserRoot:      userRoot,
			ProjectSubdir: "",
			KindDir:       "",
			Nested:        false,
			PreserveName:  true,
		},
	},
	AgentToolsShape: filedrop.ToolsYAMLArray,
}

// emitMCPServerGemini turns a *resource.MCPServer into one
// $.mcpServers.<name> JSON fragment for Gemini settings.json. Gemini
// shares the canonical mcpServers key with Claude; only the target file
// differs.
//
// Universal-core fields are emitted directly. Extension fields are
// retained only when schema/mcp-server.yaml says gemini-cli keeps that
// extension natively or the field is non-lossy pass-through metadata
// (e.g. `type`). Unknown or other-host extensions are intentionally
// dropped by emit; the orchestrator's §8 lossy gate is what decides
// whether that drop is allowed.
func emitMCPServerGemini(r resource.Resource) ([]configfrag.MergedFragment, error) {
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
	if len(m.Extensions()) > 0 {
		sc, err := schema.Load(resource.KindMCPServer)
		if err != nil {
			return nil, fmt.Errorf("schema unavailable for mcp-server re-encode: %w", err)
		}
		keys := make([]string, 0, len(m.Extensions()))
		for k := range m.Extensions() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, taken := value[k]; taken {
				continue
			}
			if !sc.HostKeepsExtension(hostID, k) {
				continue
			}
			value[k] = m.Extensions()[k]
		}
	}
	return []configfrag.MergedFragment{{
		Path:  "$.mcpServers." + m.Name,
		Value: value,
	}}, nil
}

// emitHookGemini turns a *resource.Hook into one MergedFragment per
// binding across all the resource's events. Gemini's current settings
// schema stores hooks under $.hooks.<Event>, so the path shape is the
// same JSON wrapper as Claude with Gemini event names at the leaf.
//
// Per-host divergence:
//   - PreToolUse/PostToolUse become BeforeTool/AfterTool.
//   - timeout is canonical seconds in dotpack and milliseconds in
//     Gemini settings.json.
//   - Gemini hook-spec extensions (name, description, async, once) are
//     re-emitted when present. If a spec has no name, dotpack gives it a
//     stable name derived from the hook resource name so Gemini's
//     hooksConfig.disabled list can target it later.
func emitHookGemini(r resource.Resource) ([]configfrag.MergedFragment, error) {
	h, ok := r.(*resource.Hook)
	if !ok {
		return nil, fmt.Errorf("emit hook: resource type %T is not *resource.Hook", r)
	}
	if len(h.Events) == 0 {
		return nil, fmt.Errorf("emit hook: %s has no events", h.Name)
	}
	sc, err := schema.Load(resource.KindHook)
	if err != nil {
		return nil, fmt.Errorf("schema unavailable for hook re-encode: %w", err)
	}
	totalSpecs := countHookSpecs(h)
	specOrdinal := 0
	frags := make([]configfrag.MergedFragment, 0)
	for _, ev := range h.Events {
		geminiEvent := geminiHookEvent(ev.Event)
		for _, b := range ev.Bindings {
			value, err := encodeHookBindingGemini(b, h.Name, totalSpecs, &specOrdinal, sc)
			if err != nil {
				return nil, err
			}
			frags = append(frags, configfrag.MergedFragment{
				Path:  "$.hooks." + geminiEvent,
				Value: value,
				Op:    adapter.MergedKeyAppend,
			})
		}
	}
	return frags, nil
}

func countHookSpecs(h *resource.Hook) int {
	n := 0
	for _, ev := range h.Events {
		for _, b := range ev.Bindings {
			n += len(b.Hooks)
		}
	}
	return n
}

func geminiHookEvent(event string) string {
	switch event {
	case "PreToolUse":
		return "BeforeTool"
	case "PostToolUse":
		return "AfterTool"
	default:
		return event
	}
}

func encodeHookBindingGemini(b resource.Binding, hookName string, totalSpecs int, specOrdinal *int, sc *schema.Schema) (map[string]any, error) {
	out := map[string]any{}
	if b.Matcher != "" {
		out["matcher"] = b.Matcher
	}
	specs := make([]any, 0, len(b.Hooks))
	for _, s := range b.Hooks {
		*specOrdinal++
		spec, err := encodeHookSpecGemini(s, b.Extensions, hookName, totalSpecs, *specOrdinal, sc)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	out["hooks"] = specs
	return out, nil
}

func encodeHookSpecGemini(s resource.HookSpec, bindingExt map[string]any, hookName string, totalSpecs, ordinal int, sc *schema.Schema) (map[string]any, error) {
	out := map[string]any{
		"type":    s.Type,
		"command": hookcmd.ForHost(s.Command, hookcmd.ProjectRootExpr),
	}
	if s.HasTimeout {
		out["timeout"] = s.Timeout * 1000
	}
	if s.StatusMessage != "" {
		out["statusMessage"] = s.StatusMessage
	}
	if len(s.Env) > 0 {
		out["env"] = s.Env
	}
	for _, ext := range []map[string]any{bindingExt, s.Extensions} {
		keys := make([]string, 0, len(ext))
		for k := range ext {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, taken := out[k]; taken {
				continue
			}
			if !sc.HostKeepsExtension(hostID, k) {
				continue
			}
			out[k] = ext[k]
		}
	}
	if name, _ := out["name"].(string); name == "" {
		if totalSpecs == 1 {
			out["name"] = hookName
		} else {
			out["name"] = fmt.Sprintf("%s-%d", hookName, ordinal)
		}
	}
	return out, nil
}

// configfragPolicy returns the gemini-cli configfrag policy.
func configfragPolicy() configfrag.Policy {
	return configfrag.Policy{
		HostID: hostID,
		Kinds: map[resource.Kind]configfrag.KindConfig{
			resource.KindMCPServer: {
				Format: configfrag.FormatJSON,
				Files: configfrag.ScopeFiles{
					User:    userSettingsFile,
					Project: projectSettingsFile,
				},
				Emit: emitMCPServerGemini,
			},
			resource.KindHook: {
				Format: configfrag.FormatJSON,
				Files: configfrag.ScopeFiles{
					User:    userSettingsFile,
					Project: projectSettingsFile,
				},
				Emit: emitHookGemini,
			},
		},
	}
}

// Adapter is the gemini-cli per-host shell that dispatches Plan to the
// right deep module by resource Kind.
type Adapter struct {
	filedrop   *filedrop.Adapter
	configfrag *configfrag.Adapter
}

// New constructs the gemini-cli adapter, wiring filedrop and configfrag
// policies with the given Dirs.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{
		filedrop:   filedrop.New(d, Policy),
		configfrag: configfrag.New(d, configfragPolicy()),
	}
}

// HostID returns the schema-side host alias.
func (a *Adapter) HostID() string { return hostID }

// Plan dispatches by Kind. File-drop kinds (skill, agent, rule) go to the
// filedrop adapter; config-fragment kinds (mcp-server, hook) go to
// configfrag.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch r.Kind() {
	case resource.KindSkill, resource.KindAgent, resource.KindRule, resource.KindCommand, resource.KindMemory:
		return a.filedrop.Plan(r, scope)
	case resource.KindMCPServer, resource.KindHook:
		return a.configfrag.Plan(r, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", hostID, r.Kind())
	}
}
