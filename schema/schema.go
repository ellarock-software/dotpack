package schema

import (
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/ellarock/dotpack/internal/resource"
)

// Schema is one kind's parsed YAML. Only the fields the ADR-0016 §8
// algorithm reads are unmarshalled — the rest of the YAML (template,
// fields, ecosystem_notes, reasons) is intentionally ignored here;
// validators / docs read it directly from the YAML files.
type Schema struct {
	Kind                 string    `yaml:"kind"`
	DotpackSchemaVersion int       `yaml:"dotpack_schema_version"`
	DeliberatelyExcluded []Concept `yaml:"deliberately_excluded"`
}

// Concept mirrors one entry under `deliberately_excluded:`. Each entry
// declares a canonical_concept slug, host aliases that natively support
// it, and a lossy_when_dropped flag (default true — see IsLossyWhenDropped).
type Concept struct {
	CanonicalConcept string  `yaml:"canonical_concept"`
	Aliases          []Alias `yaml:"aliases"`

	// LossyWhenDropped is *bool so we can distinguish absent (→ default
	// true per ADR-0016 §8) from explicit false (pass-through metadata).
	LossyWhenDropped *bool `yaml:"lossy_when_dropped"`

	// CanonicalisesTo is the ADR-0017 Scenario B anchor — names a
	// universal-core field this concept folds into when present under a
	// different host-specific name. Parsed and stored but not yet
	// consumed: the translator step that resolves it is deferred to
	// ADR-0017. Storing it (rather than silently dropping on parse)
	// means a future schema entry with this field won't be ignored
	// without anyone noticing.
	CanonicalisesTo string `yaml:"canonicalises_to"`
}

// Alias is one (host, field_name) pair under a concept's aliases array.
// host is the dotpack adapter HostID ("claude-code", "gemini", "codex");
// field_name is the on-disk frontmatter key as it appears in that host's
// SKILL.md (e.g. "allowed-tools"). The field_name namespace is distinct
// from the canonical_concept slug namespace — pitfall (a) in the slice-2
// handoff. LookupExtension matches by field_name, not slug.
type Alias struct {
	Host      string `yaml:"host"`
	FieldName string `yaml:"field_name"`
}

// SupportsHost reports whether the given host appears in any of this
// concept's aliases.
func (c *Concept) SupportsHost(host string) bool {
	for _, a := range c.Aliases {
		if a.Host == host {
			return true
		}
	}
	return false
}

// IsLossyWhenDropped reports whether dropping this concept on a
// non-supporting host is semantically lossy. Defaults to true per
// ADR-0016 §8: silent drop is the failure mode dotpack exists to
// prevent, so the safer default is "lossy unless explicitly opted out".
func (c *Concept) IsLossyWhenDropped() bool {
	if c.LossyWhenDropped == nil {
		return true
	}
	return *c.LossyWhenDropped
}

// SupportingHosts returns the deduped list of hosts that natively
// support this concept. Used to populate LossyReason.SupportedHosts so
// the CLI can tell the user "this field works on hosts X, Y but not
// the one you targeted".
func (c *Concept) SupportingHosts() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(c.Aliases))
	for _, a := range c.Aliases {
		if seen[a.Host] {
			continue
		}
		seen[a.Host] = true
		out = append(out, a.Host)
	}
	return out
}

// LookupExtension finds the concept whose aliases include the given
// frontmatter field name. Returns nil if no concept matches (the field
// is not in any deliberately_excluded entry — treated as unknown by
// the orchestrator's §8 algorithm).
//
// fieldName is the on-disk key (e.g. "allowed-tools"), NOT the
// canonical_concept slug. These are different namespaces.
func (s *Schema) LookupExtension(fieldName string) *Concept {
	for i := range s.DeliberatelyExcluded {
		c := &s.DeliberatelyExcluded[i]
		for _, a := range c.Aliases {
			if a.FieldName == fieldName {
				return c
			}
		}
	}
	return nil
}

// Load returns the parsed schema for the given kind. Results are cached
// per-kind; concurrent callers see the same *Schema. The file lookup is
// embed.FS-backed and cannot fail at runtime in production (the bytes
// ship with the binary); the YAML unmarshal is the only realistic error
// source.
func Load(kind resource.Kind) (*Schema, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if s, ok := cache[kind]; ok {
		return s, nil
	}
	raw, err := files.ReadFile(string(kind) + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("schema: load %q: %w", kind, err)
	}
	var s Schema
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("schema: parse %q: %w", kind, err)
	}
	cache[kind] = &s
	return &s, nil
}

var (
	cacheMu sync.Mutex
	cache   = map[resource.Kind]*Schema{}
)
