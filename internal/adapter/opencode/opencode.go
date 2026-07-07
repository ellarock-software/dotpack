// Package opencode is the OpenCode (opencode.ai) per-host shell. It is
// the first adapter onboarded after the registry refactor (ADR-0014) and
// exists partly to prove that path: it self-registers in init(), wires
// the shared filedrop + configfrag deep modules, and needed no edit to
// any core switchboard — only this package, one blank import in
// internal/adapter/all, and the OpenCodeHome field on dirs.Dirs.
//
// OpenCode deliberately lands with a PARTIAL support matrix, which is the
// point: per-operation support is genuine per-adapter data, not a global
// constant. OpenCode supports skill, agent, command, memory, and
// mcp-server; it does NOT support rule (OpenCode rules are just AGENTS.md,
// with no per-rule file) or hook (OpenCode extends behaviour via JS
// plugins, not a declarative hook config). Those two kinds are simply
// absent from the policies, so Plan returns the standard
// "kind X not yet supported" error with no special-casing.
//
// Paths (per opencode.ai/docs/config):
//   - skill:      <OpenCodeHome>/skills/<name>/SKILL.md (user) ·
//     <ProjectHome>/.opencode/skills/<name>/SKILL.md (project)
//   - agent:      <OpenCodeHome>/agents/<name>.md · <ProjectHome>/.opencode/agents/<name>.md
//   - command:    <OpenCodeHome>/commands/<name>.md · <ProjectHome>/.opencode/commands/<name>.md
//   - memory:     <OpenCodeHome>/AGENTS.md · <ProjectHome>/AGENTS.md
//   - mcp-server: <OpenCodeHome>/opencode.json (user) ·
//     <ProjectHome>/opencode.json (project), merged under $.mcp.<name> (JSON).
package opencode

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/configfrag"
	"github.com/ellarock-software/dotpack/internal/adapter/filedrop"
	"github.com/ellarock-software/dotpack/internal/adapter/registry"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/schema"
)

// hostID is the dotpack adapter HostID for OpenCode.
const hostID = "opencode"

// init self-registers the opencode adapter (ADR-0014).
func init() {
	registry.RegisterAdapter(hostID, func(d dirs.Dirs) adapter.Adapter { return New(d) })
}

// userRoot returns OpenCodeHome with a host-specific missing-dir error.
func userRoot(d dirs.Dirs) (string, error) {
	if d.OpenCodeHome == "" {
		return "", fmt.Errorf("opencode: user scope requires dirs.OpenCodeHome to be set")
	}
	return d.OpenCodeHome, nil
}

// userConfigFile returns <OpenCodeHome>/opencode.json — OpenCode's
// user-scope target for mcp-server installs.
func userConfigFile(d dirs.Dirs) (string, error) {
	if d.OpenCodeHome == "" {
		return "", fmt.Errorf("opencode: user scope requires dirs.OpenCodeHome to be set")
	}
	return filepath.Join(d.OpenCodeHome, "opencode.json"), nil
}

// projectConfigFile returns <ProjectHome>/opencode.json — OpenCode's
// project-scope config lives at the project root, not under .opencode.
func projectConfigFile(d dirs.Dirs) (string, error) {
	if d.ProjectHome == "" {
		return "", fmt.Errorf("opencode: project scope requires dirs.ProjectHome to be set")
	}
	return filepath.Join(d.ProjectHome, "opencode.json"), nil
}

// Policy is the opencode file-drop layout data. No rule kind (OpenCode
// has no per-rule file) and no hook kind (config-fragment, and OpenCode
// has no declarative hooks).
var Policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: ".opencode",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
		resource.KindAgent: {
			UserRoot:      userRoot,
			ProjectSubdir: ".opencode",
			KindDir:       "agents",
			Nested:        false,
		},
		resource.KindCommand: {
			UserRoot:      userRoot,
			ProjectSubdir: ".opencode",
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
	AgentToolsShape: filedrop.ToolsYAMLArray,
}

// emitMCPServerOpenCode turns a *resource.MCPServer into one $.mcp.<name>
// JSON fragment for opencode.json. OpenCode's schema differs from the
// Claude/Gemini mcpServers shape: it folds command+args into a single
// `command` array and tags each server with a `type` ("local" | "remote").
// This per-host emit divergence is exactly what a new adapter is free to
// express; the orchestrator's §8 lossy gate (not this function) decides
// whether any dropped extension requires --allow-lossy.
func emitMCPServerOpenCode(r resource.Resource) ([]configfrag.MergedFragment, error) {
	m, ok := r.(*resource.MCPServer)
	if !ok {
		return nil, fmt.Errorf("emit mcp-server: resource type %T is not *resource.MCPServer", r)
	}
	value := map[string]any{}
	switch {
	case m.URL != "":
		value["type"] = "remote"
		value["url"] = m.URL
	default:
		value["type"] = "local"
		command := make([]string, 0, 1+len(m.Args))
		if m.Command != "" {
			command = append(command, m.Command)
		}
		command = append(command, m.Args...)
		value["command"] = command
	}
	if len(m.Env) > 0 {
		value["environment"] = m.Env
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
		Path:  "$.mcp." + m.Name,
		Value: value,
	}}, nil
}

// configfragPolicy returns opencode's configfrag policy. Only mcp-server
// is wired (JSON into opencode.json); hook is intentionally absent.
func configfragPolicy() configfrag.Policy {
	return configfrag.Policy{
		HostID: hostID,
		Kinds: map[resource.Kind]configfrag.KindConfig{
			resource.KindMCPServer: {
				Format: configfrag.FormatJSON,
				Files: configfrag.ScopeFiles{
					User:    userConfigFile,
					Project: projectConfigFile,
				},
				Emit: emitMCPServerOpenCode,
			},
		},
	}
}

// Adapter is the opencode per-host shell.
type Adapter struct {
	filedrop   *filedrop.Adapter
	configfrag *configfrag.Adapter
}

// New constructs the opencode adapter.
func New(d dirs.Dirs) *Adapter {
	return &Adapter{
		filedrop:   filedrop.New(d, Policy),
		configfrag: configfrag.New(d, configfragPolicy()),
	}
}

// HostID returns the schema-side host alias.
func (a *Adapter) HostID() string { return hostID }

// DescribeLayouts exposes opencode's file-drop layouts for the
// registry-driven scan / help (ADR-0014).
func (a *Adapter) DescribeLayouts() []adapter.KindLayout { return a.filedrop.DescribeLayouts() }

// Plan dispatches by Kind. File-drop kinds (skill, agent, command,
// memory) go to filedrop; mcp-server goes to configfrag. rule and hook
// are unsupported and fall through to the structured error.
func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch r.Kind() {
	case resource.KindSkill, resource.KindAgent, resource.KindCommand, resource.KindMemory:
		return a.filedrop.Plan(r, scope)
	case resource.KindMCPServer:
		return a.configfrag.Plan(r, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", hostID, r.Kind())
	}
}
