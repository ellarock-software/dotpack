package schema_test

import (
	"testing"

	"github.com/ellarock/dotpack/internal/resource"
	"github.com/ellarock/dotpack/schema"
)

func TestLossyExtensions_EmptyExtensions_NoReasons(t *testing.T) {
	// Trivial baseline — a skill with no extensions never goes lossy.
	got, err := schema.LossyExtensions(resource.KindSkill, "claude-code", nil)
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d reasons, want 0", len(got))
	}
}

func TestLossyExtensions_AllowedToolsOnClaudeCode_NotLossy(t *testing.T) {
	// schema/skill.yaml lists `allowed-tools` under canonical_concept
	// claude_skill_runtime_overrides with host=claude-code in aliases.
	// Installing on claude-code → not lossy.
	got, err := schema.LossyExtensions(resource.KindSkill, "claude-code",
		map[string]any{"allowed-tools": []any{"Read"}})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no lossy reasons on claude-code; got %+v", got)
	}
}

func TestLossyExtensions_AllowedToolsOnGemini_Lossy(t *testing.T) {
	// Same concept, different target host. gemini is NOT in
	// claude_skill_runtime_overrides.aliases → lossy on gemini.
	got, err := schema.LossyExtensions(resource.KindSkill, "gemini",
		map[string]any{"allowed-tools": []any{"Read"}})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 lossy reason on gemini; got %+v", got)
	}
	if got[0].FieldPath != "allowed-tools" {
		t.Errorf("FieldPath: got %q, want allowed-tools", got[0].FieldPath)
	}
	if got[0].CanonicalConcept != "claude_skill_runtime_overrides" {
		t.Errorf("CanonicalConcept: got %q, want claude_skill_runtime_overrides", got[0].CanonicalConcept)
	}
	wantSupported := []string{"claude-code"}
	if len(got[0].SupportedHosts) != 1 || got[0].SupportedHosts[0] != wantSupported[0] {
		t.Errorf("SupportedHosts: got %v, want %v", got[0].SupportedHosts, wantSupported)
	}
}

func TestLossyExtensions_PassThroughMetadata_BindingDeferred(t *testing.T) {
	// ADR-0016 §8 specifies a pass-through skip for concepts with
	// lossy_when_dropped: false. The current schema's pass-through
	// concepts (discovery_keywords, metadata_bucket) declare empty
	// aliases — so there is no field_name → concept binding for them.
	// Until the schema gains a concept-level field_names mechanism
	// (slice-3 work), pass-through fields fall through to the unknown
	// branch and surface as lossy. Loud + recoverable per ADR-0016's
	// failure-mode-safety argument; pin the current behavior so a
	// future schema/code change is forced to deliberately update both.
	got, err := schema.LossyExtensions(resource.KindSkill, "claude-code",
		map[string]any{"keywords": []any{"tag1"}})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 1 || got[0].FieldPath != "keywords" || got[0].CanonicalConcept != "" {
		t.Errorf("expected `keywords` to surface as unknown (deferred); got %+v", got)
	}
}

func TestLossyExtensions_UnknownField_Lossy_UnknownConcept(t *testing.T) {
	// Per ADR-0016 §Why (failure-mode safety): an extension not listed
	// in any deliberately_excluded entry is lossy by default. The
	// CanonicalConcept field stays empty so the CLI can render it as
	// "(unknown field)" rather than a misleading concept slug.
	got, err := schema.LossyExtensions(resource.KindSkill, "claude-code",
		map[string]any{"totally_made_up_field": "x"})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 lossy reason; got %+v", got)
	}
	if got[0].FieldPath != "totally_made_up_field" {
		t.Errorf("FieldPath: got %q", got[0].FieldPath)
	}
	if got[0].CanonicalConcept != "" {
		t.Errorf("CanonicalConcept for unknown field: got %q, want empty", got[0].CanonicalConcept)
	}
}

func TestLossyExtensions_DeterministicOrdering(t *testing.T) {
	// Returned reasons must be sorted by field name so error messages
	// and logs are reproducible across runs (Go map iteration is
	// randomised). Mixing claude-supported (kept) with two unknowns
	// (lossy) exercises the keep/drop branching across the sorted walk.
	exts := map[string]any{
		"zzz_unknown":   "a",
		"allowed-tools": []any{"Read"}, // claude-supported → kept on claude-code
		"aaa_unknown":   "b",
	}
	got, err := schema.LossyExtensions(resource.KindSkill, "claude-code", exts)
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lossy reasons (the two unknowns); got %+v", got)
	}
	if got[0].FieldPath != "aaa_unknown" || got[1].FieldPath != "zzz_unknown" {
		t.Errorf("ordering: got [%s, %s], want [aaa_unknown, zzz_unknown]",
			got[0].FieldPath, got[1].FieldPath)
	}
}

func TestLookupExtension_FieldNameNamespace_NotConceptSlugNamespace(t *testing.T) {
	// Pitfall (a) from the slice-2 handoff: LookupExtension matches by
	// frontmatter field_name (e.g. "allowed-tools"), NOT by the
	// canonical_concept slug (e.g. "claude_skill_runtime_overrides").
	// A future refactor that conflates the two namespaces would make
	// every per-instance lossy check misfire silently. Pin both
	// directions: the field name hits, the slug misses.
	s, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := s.LookupExtension("allowed-tools"); got == nil {
		t.Error("LookupExtension(\"allowed-tools\") = nil; want claude_skill_runtime_overrides concept")
	} else if got.CanonicalConcept != "claude_skill_runtime_overrides" {
		t.Errorf("matched concept: got %q, want claude_skill_runtime_overrides", got.CanonicalConcept)
	}

	if got := s.LookupExtension("claude_skill_runtime_overrides"); got != nil {
		t.Errorf("LookupExtension(\"claude_skill_runtime_overrides\") = %+v; want nil — slug is not a field name", got)
	}
}

func TestLoad_CachesPerKind(t *testing.T) {
	// Sanity check on caching: two loads return the same pointer.
	a, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load 1: %v", err)
	}
	b, err := schema.Load(resource.KindSkill)
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if a != b {
		t.Errorf("Load is not caching: got distinct *Schema pointers")
	}
}
