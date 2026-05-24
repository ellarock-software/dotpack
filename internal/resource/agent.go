package resource

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Agent mirrors schema/agent.yaml's universal core (name, description,
// model, tools) plus a Body (the markdown body that becomes the agent's
// system prompt) and host-specific frontmatter fields the per-instance
// lossy check inspects (access via Extensions()).
//
// Tools is normalised to []string regardless of source shape. The schema
// permits both the Claude-convention comma-separated string
// ("Read, Write, Edit") and the Gemini-convention YAML array
// ([read_file, grep_search]); ParseAgent collapses both into the same
// canonical form so adapters can re-emit per host preference. Raw bytes
// preserve the original shape for byte-pass-through callers that would
// otherwise care — but planAgent on claude-code does NOT pass-through
// (see claudecode.planAgent), so this distinction only matters once a
// future kind or host needs it.
//
// Raw is the original on-disk bytes the parser was given (cache copy
// guarantee from ADR-0008). Translator-produced Agents leave it nil.
type Agent struct {
	Name        string
	Description string
	Model       string
	Tools       []string
	Body        string
	Raw         []byte
	extensions  map[string]any
}

// Kind returns KindAgent so *Agent satisfies the Resource interface.
func (a *Agent) Kind() Kind { return KindAgent }

// Extensions returns the agent's host-extension frontmatter fields.
// Required by the resource.Resource interface; the orchestrator's
// schema-driven §8 lossy detection walks this map. Returns nil when
// the source had only universal-core fields.
func (a *Agent) Extensions() map[string]any { return a.extensions }

// ResourceName returns the agent's name field (Named interface).
func (a *Agent) ResourceName() string { return a.Name }

// WithExtensions sets the extension map and returns the receiver. Drops
// Raw so the byte-pass-through invariant cannot lie about a stale Raw
// vs. mutated extensions (same shape as Skill.WithExtensions).
func (a *Agent) WithExtensions(m map[string]any) *Agent {
	a.extensions = m
	a.Raw = nil
	return a
}

// ParseAgent parses agent frontmatter (the bytes between the first two
// `---` lines, then markdown body) into an *Agent. Tools is normalised
// to []string regardless of source shape — comma-separated string or
// YAML array. Per ADR-0008, Raw is preserved so a byte-identical
// pass-through path could be added later; today's claudecode.planAgent
// always re-encodes so the tools form is the host's preferred shape.
func ParseAgent(raw []byte) (*Agent, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}

	fields := map[string]any{}
	if err := yaml.Unmarshal(front, &fields); err != nil {
		return nil, fmt.Errorf("agent: parse frontmatter: %w", err)
	}

	agent := &Agent{Body: string(body), Raw: append([]byte(nil), raw...)}
	for key, val := range fields {
		switch key {
		case "name":
			agent.Name, _ = val.(string)
		case "description":
			agent.Description, _ = val.(string)
		case "model":
			agent.Model, _ = val.(string)
		case "tools":
			tools, err := normaliseTools(val)
			if err != nil {
				return nil, fmt.Errorf("agent: %w", err)
			}
			agent.Tools = tools
		default:
			if agent.extensions == nil {
				agent.extensions = map[string]any{}
			}
			agent.extensions[key] = val
		}
	}
	return agent, nil
}

// normaliseTools collapses the schema's two accepted tools shapes
// (comma-separated string per Claude convention, YAML array per Gemini
// convention) into the canonical []string. Both forms appear in the
// wild per schema/agent.yaml notes; the orchestrator treats tools as a
// universal-core field, so the canonical form lives in one place.
//
// Errors loudly on any other shape (number, map, etc.) — silently
// returning nil here would let install proceed with an empty tool list,
// which is a security-relevant default that the user did not intend.
func normaliseTools(val any) ([]string, error) {
	switch v := val.(type) {
	case nil:
		return nil, nil
	case string:
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("tools[%d]: expected string, got %T", i, item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tools: expected string or array, got %T", val)
	}
}
