// Package hermes is the Hermes Agent per-host shell. Hermes is an honest
// PARTIAL adapter:
//
//   - skill: user scope only, at ~/.hermes/skills/<name>/SKILL.md
//   - memory: project scope at repo-root context files (AGENTS.md /
//     .hermes.md / HERMES.md / CLAUDE.md), user scope at ~/.hermes/SOUL.md
//   - mcp-server: user scope only, merged into ~/.hermes/config.yaml under
//     mcp_servers.<name>
//   - hook: user scope only, merged into ~/.hermes/config.yaml under
//     hooks.<event>
//
// Hermes has no native dotpack file surfaces for agent, rule, or command.
// Those kinds stay unsupported and surface the standard "kind X not yet
// supported" error.
package hermes

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ellarock-software/dotpack/internal/adapter"
	"github.com/ellarock-software/dotpack/internal/adapter/configfrag"
	"github.com/ellarock-software/dotpack/internal/adapter/filedrop"
	"github.com/ellarock-software/dotpack/internal/adapter/hookcmd"
	"github.com/ellarock-software/dotpack/internal/adapter/registry"
	"github.com/ellarock-software/dotpack/internal/dirs"
	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/schema"
)

const hostID = "hermes"

func init() {
	registry.RegisterAdapter(hostID, func(d dirs.Dirs) adapter.Adapter { return New(d) })
}

func userRoot(d dirs.Dirs) (string, error) {
	if d.HermesHome == "" {
		return "", fmt.Errorf("hermes: user scope requires dirs.HermesHome to be set")
	}
	return d.HermesHome, nil
}

func userConfigFile(d dirs.Dirs) (string, error) {
	if d.HermesHome == "" {
		return "", fmt.Errorf("hermes: user scope requires dirs.HermesHome to be set")
	}
	return filepath.Join(d.HermesHome, "config.yaml"), nil
}

var policy = filedrop.Policy{
	HostID: hostID,
	Layouts: map[resource.Kind]filedrop.Layout{
		resource.KindSkill: {
			UserRoot:      userRoot,
			ProjectSubdir: "",
			KindDir:       "skills",
			Nested:        true,
			NestedFile:    "SKILL.md",
		},
		resource.KindMemory: {
			UserRoot:      userRoot,
			ProjectSubdir: "",
			KindDir:       "",
			Nested:        false,
			PreserveName:  true,
		},
	},
}

func emitMCPServerHermes(r resource.Resource) ([]configfrag.MergedFragment, error) {
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
		keys := sortedAnyKeys(m.Extensions())
		for _, k := range keys {
			targetKey := k
			if k == "http_headers" {
				targetKey = "headers"
			}
			if _, taken := value[targetKey]; taken {
				continue
			}
			if k == "tools" {
				if !isHermesMCPToolsPolicy(m.Extensions()[k]) {
					return nil, fmt.Errorf("emit mcp-server: %s.tools is not a Hermes tools policy mapping", m.Name)
				}
			}
			if !sc.HostKeepsExtension(hostID, k) {
				continue
			}
			value[targetKey] = m.Extensions()[k]
		}
	}
	return []configfrag.MergedFragment{{
		Path:  "mcp_servers." + m.Name,
		Value: value,
	}}, nil
}

func emitHookHermes(r resource.Resource) ([]configfrag.MergedFragment, error) {
	h, ok := r.(*resource.Hook)
	if !ok {
		return nil, fmt.Errorf("emit hook: resource type %T is not *resource.Hook", r)
	}
	if len(h.Events) == 0 {
		return nil, fmt.Errorf("emit hook: %s has no events", h.Name)
	}
	frags := make([]configfrag.MergedFragment, 0)
	for _, ev := range h.Events {
		hermesEvent, ok := hermesHookEvent(ev.Event)
		if !ok {
			return nil, fmt.Errorf("emit hook: Hermes has no native shell-hook event for canonical event %q", ev.Event)
		}
		for _, b := range ev.Bindings {
			if b.Matcher != "" && hermesEvent != "pre_tool_call" && hermesEvent != "post_tool_call" {
				return nil, fmt.Errorf("emit hook: Hermes only supports matcher on tool-call events; %s carried matcher %q", ev.Event, b.Matcher)
			}
			for _, s := range b.Hooks {
				value := map[string]any{
					"command": hermesHookCommand(s),
				}
				if b.Matcher != "" {
					value["matcher"] = b.Matcher
				}
				if s.HasTimeout {
					value["timeout"] = s.Timeout
				}
				frags = append(frags, configfrag.MergedFragment{
					Path:  "hooks." + hermesEvent,
					Value: value,
					Op:    adapter.MergedKeyAppend,
				})
			}
		}
	}
	return frags, nil
}

func hermesHookEvent(event string) (string, bool) {
	switch event {
	case "PreToolUse":
		return "pre_tool_call", true
	case "PostToolUse":
		return "post_tool_call", true
	case "UserPromptSubmit":
		return "pre_llm_call", true
	case "SessionStart":
		return "on_session_start", true
	case "SessionEnd":
		return "on_session_end", true
	case "SubagentStart":
		return "subagent_start", true
	case "SubagentStop":
		return "subagent_stop", true
	case "PermissionRequest":
		return "pre_approval_request", true
	default:
		return "", false
	}
}

func hermesHookCommand(s resource.HookSpec) string {
	command := hookcmd.ForHost(s.Command, hookcmd.ProjectRootExpr)
	if needsHermesShell(command) {
		command = "bash -lc " + shellQuote(command)
	}
	if len(s.Env) == 0 {
		return command
	}
	keys := sortedStringKeys(s.Env)
	parts := make([]string, 0, 1+len(keys)+1)
	parts = append(parts, "env")
	for _, k := range keys {
		parts = append(parts, k+"="+shellQuote(s.Env[k]))
	}
	parts = append(parts, command)
	return strings.Join(parts, " ")
}

func needsHermesShell(command string) bool {
	if strings.ContainsAny(command, "$`|&;<>*\n\r\t~") {
		return true
	}
	first := command
	if i := strings.IndexAny(command, " "); i >= 0 {
		first = command[:i]
	}
	if eq := strings.IndexByte(first, '='); eq > 0 {
		for _, r := range first[:eq] {
			if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
		return true
	}
	return false
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func isHermesMCPToolsPolicy(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k, val := range m {
		switch k {
		case "include", "exclude":
			if !isStringOrStringList(val) {
				return false
			}
		case "resources", "prompts":
			switch val.(type) {
			case bool, string:
				// bool-like per Hermes docs.
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func isStringOrStringList(v any) bool {
	switch vv := v.(type) {
	case string:
		return true
	case []string:
		return true
	case []any:
		for _, item := range vv {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func configfragPolicy() configfrag.Policy {
	return configfrag.Policy{
		HostID: hostID,
		Kinds: map[resource.Kind]configfrag.KindConfig{
			resource.KindMCPServer: {
				Format: configfrag.FormatYAML,
				Files: configfrag.ScopeFiles{
					User: userConfigFile,
				},
				Emit: emitMCPServerHermes,
			},
			resource.KindHook: {
				Format: configfrag.FormatYAML,
				Files: configfrag.ScopeFiles{
					User: userConfigFile,
				},
				Emit: emitHookHermes,
			},
		},
	}
}

type Adapter struct {
	filedrop   *filedrop.Adapter
	configfrag *configfrag.Adapter
}

func New(d dirs.Dirs) *Adapter {
	return &Adapter{
		filedrop:   filedrop.New(d, policy),
		configfrag: configfrag.New(d, configfragPolicy()),
	}
}

func (a *Adapter) HostID() string { return hostID }

func (a *Adapter) Plan(r resource.Resource, scope adapter.Scope) (adapter.InstallPlan, error) {
	switch v := r.(type) {
	case *resource.Skill:
		if scope == adapter.ScopeProject {
			return adapter.InstallPlan{}, fmt.Errorf("%s: scope %q not supported for this kind (Hermes only documents user-scope skills at ~/.hermes/skills)", hostID, scope)
		}
		return a.filedrop.Plan(v, scope)
	case *resource.Memory:
		return a.planMemory(v, scope)
	case *resource.MCPServer:
		if scope == adapter.ScopeProject {
			return adapter.InstallPlan{}, fmt.Errorf("%s: scope %q not supported for this kind (Hermes MCP config is user-scoped in ~/.hermes/config.yaml)", hostID, scope)
		}
		return a.configfrag.Plan(v, scope)
	case *resource.Hook:
		if scope == adapter.ScopeProject {
			return adapter.InstallPlan{}, fmt.Errorf("%s: scope %q not supported for this kind (Hermes shell-hook config is user-scoped in ~/.hermes/config.yaml)", hostID, scope)
		}
		return a.configfrag.Plan(v, scope)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("%s: kind %q not yet supported", hostID, r.Kind())
	}
}

func (a *Adapter) planMemory(m *resource.Memory, scope adapter.Scope) (adapter.InstallPlan, error) {
	clone := *m
	switch scope {
	case adapter.ScopeUser:
		clone.Name = "SOUL.md"
	case adapter.ScopeProject:
		clone.Name = hermesProjectMemoryFilename(m.Name)
	default:
		return adapter.InstallPlan{}, fmt.Errorf("%s: unknown scope %q", hostID, scope)
	}
	return a.filedrop.Plan(&clone, scope)
}

func hermesProjectMemoryFilename(name string) string {
	switch name {
	case ".hermes.md", "HERMES.md", "AGENTS.md", "CLAUDE.md":
		return name
	default:
		return "AGENTS.md"
	}
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
