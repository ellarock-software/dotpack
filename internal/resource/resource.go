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
type Resource interface {
	Kind() Kind
}

// Kind returns KindSkill so *Skill satisfies the Resource interface.
func (s *Skill) Kind() Kind { return KindSkill }
