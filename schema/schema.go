package schema

//go:generate go run ../cmd/gendocs
import (
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/ellarock/dotpack/internal/resource"
)

// Schema is one kind's parsed YAML. Only the fields the ADR-0012 §8
// algorithm reads are unmarshalled — the rest of the YAML (template,
// fields, ecosystem_notes, reasons) is intentionally ignored here;
// validators / docs read it directly from the YAML files.
type Schema struct {
	Kind                 string    `yaml:"kind"`
	DotpackSchemaVersion int       `yaml:"dotpack_schema_version"`
	DeliberatelyExcluded []Concept `yaml:"deliberately_excluded"`
}

// Concept mirrors one entry under `deliberately_excluded:`. Each entry
// declares a canonical_concept slug, the on-disk frontmatter field
// name(s) that encode it (via Aliases for hosted concepts and/or
// FieldNames for pass-through), and a lossy_when_dropped flag (default
// true — see IsLossyWhenDropped).
type Concept struct {
	CanonicalConcept string  `yaml:"canonical_concept"`
	Aliases          []Alias `yaml:"aliases"`

	// FieldNames binds on-disk frontmatter keys to concepts that no
	// host parses natively — pass-through metadata buckets, discovery
	// keywords, etc. The Aliases-only path would force such concepts
	// to invent a fake host entry; FieldNames keeps the binding clean.
	// LookupExtension checks this list in addition to Aliases[].FieldName.
	FieldNames []string `yaml:"field_names"`

	// LossyWhenDropped is *bool so we can distinguish absent (→ default
	// true per ADR-0012 §8) from explicit false (pass-through metadata).
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
// host is the dotpack adapter HostID ("claude-code", "gemini-cli",
// "codex") — MUST match the adapter's HostID() return verbatim, since
// LossyExtensions compares by string equality. A mismatch silently
// flips a host's native concepts to lossy on the host's own adapter.
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
// ADR-0012 §8: silent drop is the failure mode dotpack exists to
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

// HostKeepsExtension reports whether the given host's emit should retain
// the named extension field. The schema is the authority for this
// question — adapters delegate so the three per-host predicates that
// used to live in claudecode / gemini / codex collapse to one rule.
//
// Semantic complement to LossyExtensions: HostKeepsExtension answers
// "should the adapter emit this field?", LossyExtensions answers
// "should the user be warned about dropping this field?". Same schema
// data, different audiences. Signatures intentionally diverge —
// HostKeepsExtension is a method on an already-loaded *Schema because
// adapters call it in a re-encode loop and would otherwise reload per
// key; LossyExtensions is top-level because the orchestrator's caller
// site has only the kind, not a loaded schema.
//
// fieldName is the on-disk frontmatter key (e.g. "allowed-tools"),
// matching LookupExtension's contract. Universal-core fields (name,
// description, etc.) are NOT extensions and are not the domain of this
// method — they are always emitted.
func (s *Schema) HostKeepsExtension(hostID, fieldName string) bool {
	c := s.LookupExtension(fieldName)
	if c == nil {
		return false
	}
	if !c.IsLossyWhenDropped() {
		return true
	}
	return c.SupportsHost(hostID)
}

// LookupExtension finds the concept that binds the given on-disk
// frontmatter key, checking both Aliases[].FieldName (hosted concepts)
// and FieldNames (pass-through metadata bindings). Returns nil if no
// concept matches — the field is unknown and the orchestrator's §8
// algorithm classifies it lossy by default.
//
// fieldName is the on-disk key (e.g. "allowed-tools", "keywords"),
// NOT the canonical_concept slug. These are different namespaces.
func (s *Schema) LookupExtension(fieldName string) *Concept {
	for i := range s.DeliberatelyExcluded {
		c := &s.DeliberatelyExcluded[i]
		for _, name := range c.FieldNames {
			if name == fieldName {
				return c
			}
		}
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
