package resource

import (
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Rule is a named Markdown instruction fragment. Canonical rule sources
// live under .agents/rules/<name>.md and carry YAML introduction
// metadata before their operational Markdown body.
//
// Raw is preserved so adapters can materialize host-native rule files
// byte-for-byte when no translation is needed. SourcePath is optional
// provenance used by project-scope adapters to remove legacy
// host-specific canonical rule copies such as .agents/rules/gemini/foo.md
// after the shared .agents/rules/foo.md source replaces them.
type Rule struct {
	ID         string
	Name       string
	Body       string
	Raw        []byte
	SourcePath string
	extensions map[string]any
}

// Extensions returns rule frontmatter metadata outside the identity
// fields. Introduction metadata is schema-known pass-through data, so
// it does not force --allow-lossy and is preserved on re-encode.
func (r *Rule) Extensions() map[string]any { return r.extensions }

// NameOrID is the stable install identity for a rule.
func (r *Rule) NameOrID() string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

// WithSourcePath records the absolute source path when it is available
// at CLI load time.
func (r *Rule) WithSourcePath(source string) *Rule {
	if abs, err := filepath.Abs(source); err == nil {
		r.SourcePath = abs
	} else {
		r.SourcePath = source
	}
	return r
}

// WithExtensions sets the extension map and returns the receiver.
// Mutating extensions invalidates Raw as a byte-identical source copy.
func (r *Rule) WithExtensions(m map[string]any) *Rule {
	r.extensions = m
	r.Raw = nil
	return r
}

// ParseRule parses a Markdown rule with YAML frontmatter. `id` and
// `name` are identity fields; all other frontmatter is preserved as
// schema-inspected metadata.
func ParseRule(raw []byte) (*Rule, error) {
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("rule: %w", err)
	}

	fields := map[string]any{}
	if err := yaml.Unmarshal(front, &fields); err != nil {
		return nil, fmt.Errorf("rule: parse frontmatter: %w", err)
	}

	rule := &Rule{Body: string(body), Raw: append([]byte(nil), raw...)}
	for key, val := range fields {
		switch key {
		case "id":
			rule.ID, _ = val.(string)
		case "name":
			rule.Name, _ = val.(string)
		default:
			if rule.extensions == nil {
				rule.extensions = map[string]any{}
			}
			rule.extensions[key] = val
		}
	}
	return rule, nil
}
