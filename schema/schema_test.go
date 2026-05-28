package schema_test

import (
	"testing"

	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
)

// HostKeepsExtension consolidates the three per-host <host>Keeps
// predicates that previously lived in claudecode / gemini / codex. The
// schema is the authority for "does this host's emit retain this
// extension field?" — adapters delegate.
//
// The tracer-bullet case: claude-code IS in allowed-tools' aliases, so
// the method returns true. This pins the method exists, returns bool,
// and reads aliases the way the per-host predicates used to.

func TestHostKeepsExtension_HostInAliases_True(t *testing.T) {
	s, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.HostKeepsExtension("claude-code", "allowed-tools") {
		t.Error("HostKeepsExtension(claude-code, allowed-tools) = false; want true (claude-code is in claude_skill_runtime_overrides aliases)")
	}
}

func TestHostKeepsExtension_HostNotInAliases_False(t *testing.T) {
	// Same allowed-tools field, different target host. gemini-cli is NOT
	// in claude_skill_runtime_overrides.aliases → emit should drop it.
	// Mirror of the negative arm of the deleted per-host predicates this
	// method consolidates.
	s, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.HostKeepsExtension("gemini-cli", "allowed-tools") {
		t.Error("HostKeepsExtension(gemini-cli, allowed-tools) = true; want false (gemini-cli not in claude_skill_runtime_overrides aliases)")
	}
}

func TestHostKeepsExtension_UnknownField_FalseAndNoPanic(t *testing.T) {
	// An extension key that no schema entry binds (typo, removed adapter,
	// future field not yet surveyed). Must return false and must NOT
	// panic — adapters call this in a re-encode loop and a single typo'd
	// field cannot bring down the install.
	s, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("HostKeepsExtension panicked on unknown field: %v", r)
		}
	}()
	if s.HostKeepsExtension("claude-code", "totally_made_up_field") {
		t.Error("HostKeepsExtension(claude-code, unknown) = true; want false (unknown fields are lossy per ADR-0012 §Why failure-mode safety)")
	}
}

func TestHostKeepsExtension_PassThroughMetadata_TrueRegardlessOfHost(t *testing.T) {
	// Pass-through concepts (lossy_when_dropped: false — keywords,
	// discovery metadata) carry no host-specific runtime semantics, so
	// every host's emit should retain them. Mirror of the
	// IsLossyWhenDropped guard in the original <host>Keeps predicates.
	// Iterating multiple hosts (including a synthetic one) pins that the
	// pass-through branch fires BEFORE the SupportsHost check.
	s, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, host := range []string{"claude-code", "gemini-cli", "codex", "made-up-host"} {
		if !s.HostKeepsExtension(host, "keywords") {
			t.Errorf("HostKeepsExtension(%s, keywords) = false; want true (pass-through metadata is always kept)", host)
		}
	}
}

func TestHostKeepsExtension_SlugIsNotFieldName_PublicSurfaceDefendsTheNamespace(t *testing.T) {
	// Pitfall (a) from the slice-2 handoff: field_name and
	// canonical_concept live in distinct namespaces. LookupExtension is
	// already pinned for this in lossy_test.go, but HostKeepsExtension
	// is the new public surface adapters consume — a future refactor that
	// takes *Concept directly or does its own lookup would silently
	// reintroduce slug-conflation without any test resistance. Pin the
	// contract at the consumer-facing method too: passing a slug must NOT
	// be treated as a valid extension key.
	s, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Sanity that "allowed-tools" (the field name) IS kept on claude-code.
	if !s.HostKeepsExtension("claude-code", "allowed-tools") {
		t.Fatal("setup: claude-code should keep allowed-tools")
	}
	// The slug for that same concept must NOT be treated as a field —
	// LookupExtension returns nil for slugs, so HostKeepsExtension's
	// unknown-field branch fires and returns false.
	if s.HostKeepsExtension("claude-code", "claude_skill_runtime_overrides") {
		t.Error("HostKeepsExtension(claude-code, claude_skill_runtime_overrides) = true; want false — canonical_concept slug is not a frontmatter field name")
	}
}

func TestHostKeepsExtension_AgentKind_HostSpecificAliasesHonoured(t *testing.T) {
	// The four skill-kind tests above don't exercise agent-kind data
	// through HostKeepsExtension, but the method is generic over kind and
	// claudecode/gemini both consume it from reencodeAgent. A schema
	// change that breaks agent extensions should fail at the schema test
	// layer, not only at the adapter test layer.
	//
	// schema/agent.yaml lists claude_subagent_runtime_overrides
	// (maxTurns, disallowedTools, etc.) with ONLY claude-code in
	// aliases. The two assertions pin both arms of the predicate on
	// agent-kind data: kept on claude-code, dropped on gemini-cli.
	s, err := schema.Load(resource.KindAgent)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.HostKeepsExtension("claude-code", "disallowedTools") {
		t.Error("HostKeepsExtension(claude-code, disallowedTools) = false; want true (claude-code is in claude_subagent_runtime_overrides aliases on agent schema)")
	}
	if s.HostKeepsExtension("gemini-cli", "disallowedTools") {
		t.Error("HostKeepsExtension(gemini-cli, disallowedTools) = true; want false (gemini-cli not in claude_subagent_runtime_overrides aliases)")
	}
}
