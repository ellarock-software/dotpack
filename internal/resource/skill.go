// Package resource holds the canonical-form in-memory representations of
// dotpack resources (skill, agent, command, memory, hook, mcp-server),
// per ADR-0012 §3. Each kind is a typed struct mirroring its schema.
package resource

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Skill mirrors schema/skill.yaml's universal core (name, description,
// license) plus a Body (the markdown body of SKILL.md that the host
// loads on trigger) and host-specific frontmatter fields the per-instance
// lossy check inspects (access via Extensions()).
//
// Raw is the original SKILL.md bytes the parser was given, kept so
// adapters can satisfy ADR-0004's "byte-identical to the cache copy"
// guarantee without re-encoding. ParseSkill always populates it;
// translator-produced Skills (where there is no source file) leave it
// nil and the adapter falls back to re-encoding the universal core.
//
// Invariant: when Raw is non-empty, Raw and the extension map are
// consistent — the map reflects every non-universal-core frontmatter
// key in Raw. Code that mutates extensions without rewriting Raw
// breaks the ADR-0004 byte-pass-through guarantee (canPassThrough
// would emit Raw bytes that no longer match the in-memory extension
// set). Mutators (WithExtensions etc.) drop Raw to preserve this.
type Skill struct {
	Name        string
	Description string
	License     string
	Body        string
	Raw         []byte
	extensions  map[string]any
}

// Extensions returns the skill's host-extension frontmatter fields.
// Required by the resource.Resource interface; the orchestrator's
// schema-driven §8 lossy detection walks this map without per-kind
// type switching (so a newly-added kind cannot silently bypass §8 —
// missing Extensions() is a compile error).
//
// Returns nil when the source SKILL.md had only universal-core fields.
func (s *Skill) Extensions() map[string]any { return s.extensions }

// WithExtensions sets the extension map and returns the receiver for
// chaining. Used by tests and translator code; drops Raw so the
// Raw/extensions invariant on Skill is preserved (any future
// byte-pass-through emit will go through the re-encode path).
func (s *Skill) WithExtensions(m map[string]any) *Skill {
	s.extensions = m
	s.Raw = nil
	return s
}

// ParseSkill parses SKILL.md bytes (YAML frontmatter delimited by `---`
// lines, followed by a markdown body) into a *Skill. The bytes are not
// re-encoded by dotpack on install — per ADR-0004, drop-file resources
// are byte-identical to their cache copy; ParseSkill exists for
// validation, not for round-tripping.
func ParseSkill(raw []byte) (*Skill, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("skill: %w", err)
	}

	fields := map[string]any{}
	if err := yaml.Unmarshal(front, &fields); err != nil {
		return nil, fmt.Errorf("skill: parse frontmatter: %w", err)
	}

	skill := &Skill{Body: string(body), Raw: append([]byte(nil), raw...)}
	for key, val := range fields {
		switch key {
		case "name":
			skill.Name, _ = val.(string)
		case "description":
			skill.Description, _ = val.(string)
		case "license":
			skill.License, _ = val.(string)
		default:
			if skill.extensions == nil {
				skill.extensions = map[string]any{}
			}
			skill.extensions[key] = val
		}
	}
	return skill, nil
}

// splitFrontmatter separates YAML frontmatter (the bytes between the
// first two `---` lines) from the markdown body. Returns an error if
// the document does not begin with `---` or has no closing `---`.
func splitFrontmatter(raw []byte) (front, body []byte, err error) {
	const delim = "---"
	lines := bytes.SplitN(raw, []byte("\n"), 2)
	if len(lines) < 2 || string(bytes.TrimRight(lines[0], "\r")) != delim {
		return nil, nil, fmt.Errorf("missing opening %q line", delim)
	}
	rest := lines[1]
	endIdx := bytes.Index(rest, []byte("\n"+delim))
	if endIdx == -1 {
		return nil, nil, fmt.Errorf("missing closing %q line", delim)
	}
	front = rest[:endIdx]
	afterClose := rest[endIdx+len("\n"+delim):]
	if len(afterClose) > 0 && afterClose[0] == '\n' {
		afterClose = afterClose[1:]
	}
	return front, afterClose, nil
}
