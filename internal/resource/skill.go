// Package resource holds the canonical-form in-memory representations of
// dotpack resources (skill, agent, command, memory, hook, mcp-server),
// per ADR-0016 §3. Each kind is a typed struct mirroring its schema.
package resource

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Skill mirrors schema/skill.yaml's universal core (name, description,
// license) plus a Body (the markdown body of SKILL.md that the host
// loads on trigger) and Extensions (host-specific frontmatter fields
// the per-instance lossy check inspects).
//
// Raw is the original SKILL.md bytes the parser was given, kept so
// adapters can satisfy ADR-0008's "byte-identical to the cache copy"
// guarantee without re-encoding. ParseSkill always populates it;
// translator-produced Skills (where there is no source file) leave it
// nil and the adapter falls back to re-encoding the universal core.
type Skill struct {
	Name        string
	Description string
	License     string
	Body        string
	Extensions  map[string]any
	Raw         []byte
}

// ParseSkill parses SKILL.md bytes (YAML frontmatter delimited by `---`
// lines, followed by a markdown body) into a *Skill. The bytes are not
// re-encoded by dotpack on install — per ADR-0008, drop-file resources
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
			if skill.Extensions == nil {
				skill.Extensions = map[string]any{}
			}
			skill.Extensions[key] = val
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
