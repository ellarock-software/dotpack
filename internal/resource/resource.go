package resource

// Kind identifies one of the six resource categories from
// docs/adr/0007-... — skill, agent, command, memory, hook, mcp-server.
type Kind string

const (
	KindSkill     Kind = "skill"
	KindAgent     Kind = "agent"
	KindCommand   Kind = "command"
	KindMemory    Kind = "memory"
	KindHook      Kind = "hook"
	KindMCPServer Kind = "mcp-server"
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

// Kind returns KindSkill so *Skill satisfies the Resource interface.
// Extensions() lives in skill.go alongside its backing field.
func (s *Skill) Kind() Kind { return KindSkill }
