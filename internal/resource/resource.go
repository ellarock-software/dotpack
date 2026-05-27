package resource

// Kind identifies one of the resource categories from docs/adr/0007-...
// — skill, agent, command, memory, hook, mcp-server, rule.
type Kind string

const (
	KindSkill     Kind = "skill"
	KindAgent     Kind = "agent"
	KindCommand   Kind = "command"
	KindMemory    Kind = "memory"
	KindHook      Kind = "hook"
	KindMCPServer Kind = "mcp-server"
	KindRule      Kind = "rule"
)

// Resource is the marker interface every per-kind struct implements,
// so adapters can take a generic *resource.Resource and switch on
// Kind() to dispatch per-kind emit logic. Per ADR-0016 §3 the canonical
// form is typed Go structs, not maps — but the Adapter interface itself
// is generic over Kind, so a marker interface is the bridge.
//
// Extensions returns the per-instance frontmatter / config fields the
// ADR-0016 §8 lossy-detection algorithm inspects. Kinds that have no
// host-extensible surface (or have not yet been wired up) return nil.
// The orchestrator walks this rather than type-switching on the kind
// so that a newly-added kind cannot silently bypass §8 — forgetting to
// implement Extensions is a compile error.
type Resource interface {
	Kind() Kind
	Extensions() map[string]any
}

// Named is the optional interface for kinds whose identity is a `name:`
// field in their canonical form (skill, agent, command). Kinds with
// non-name identifiers (memory by filename + scope, mcp-server by the
// outer JSON-object key) compose their identifier elsewhere and do not
// implement Named — the orchestrator's buildRecord type-asserts and
// will need an alternate ID-derivation path when those kinds land.
//
// The method is ResourceName, not Name, to avoid the Go field/method
// name clash on existing struct fields named Name. Keeps construction
// ergonomic (`&Skill{Name: "x"}` continues to work).
type Named interface {
	ResourceName() string
}

// Kind returns KindSkill so *Skill satisfies the Resource interface.
// Extensions() lives in skill.go alongside its backing field.
func (s *Skill) Kind() Kind { return KindSkill }

// ResourceName returns the skill's name field (Named interface).
func (s *Skill) ResourceName() string { return s.Name }

// Kind returns KindHook so *Hook satisfies the Resource interface.
// Extensions() lives in hook.go alongside its backing field.
func (h *Hook) Kind() Kind { return KindHook }

// ResourceName returns the hook bundle's name (filename-derived per
// loadResource — hook source has no in-source name field, mirroring
// mcp-server's map-key identity but with the filesystem as the
// identity carrier when the source format does not encode one).
func (h *Hook) ResourceName() string { return h.Name }

// Kind returns KindRule so *Rule satisfies the Resource interface.
// Extensions() lives in rule.go alongside its backing field.
func (r *Rule) Kind() Kind { return KindRule }

// ResourceName returns the rule's name, falling back to id when the
// canonical source only carries `id:`.
func (r *Rule) ResourceName() string { return r.NameOrID() }
