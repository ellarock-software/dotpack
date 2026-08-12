package schema_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ellarock-software/dotpack/internal/resource"
	"github.com/ellarock-software/dotpack/schema"
)

// yamlUnmarshal is a thin alias kept for clarity in tests that build
// synthetic Schema values bypassing the embed.FS (e.g. ADR-0017
// Scenario B coverage where no production entry yet uses the field).
func yamlUnmarshal(data []byte, out any) error { return yaml.Unmarshal(data, out) }

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

func TestLossyExtensions_AllowedToolsOnGeminiCLI_Lossy(t *testing.T) {
	// Same concept, different target host. gemini-cli is NOT in
	// claude_skill_runtime_overrides.aliases → lossy on gemini-cli.
	// (Pre-slice-3-#7 this used bare "gemini" as a synthetic non-claude
	// host. Now that gemini-cli is a real adapter, the test uses the
	// real HostID — the synthetic case is subsumed.)
	got, err := schema.LossyExtensions(resource.KindSkill, "gemini-cli",
		map[string]any{"allowed-tools": []any{"Read"}})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 lossy reason on gemini-cli; got %+v", got)
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

func TestLossyExtensions_PassThroughMetadata_NeverLossy(t *testing.T) {
	// ADR-0012 §8 pass-through skip: concepts with lossy_when_dropped:
	// false (discovery_keywords, metadata_bucket) are never lossy on
	// any host, because dropping them changes nothing observable.
	// The schema binds the on-disk field names via Concept.FieldNames
	// (separate from aliases[].host, which is for hosted concepts).
	for _, host := range []string{"claude-code", "gemini-cli", "codex", "made-up-host"} {
		got, err := schema.LossyExtensions(resource.KindSkill, host,
			map[string]any{"keywords": []any{"tag1"}})
		if err != nil {
			t.Fatalf("LossyExtensions(keywords) on %s: %v", host, err)
		}
		if len(got) != 0 {
			t.Errorf("host=%s keywords: expected no lossy (pass-through); got %+v", host, got)
		}
		got2, err := schema.LossyExtensions(resource.KindSkill, host,
			map[string]any{"metadata": map[string]any{"short-description": "x"}})
		if err != nil {
			t.Fatalf("LossyExtensions(metadata) on %s: %v", host, err)
		}
		if len(got2) != 0 {
			t.Errorf("host=%s metadata: expected no lossy (pass-through); got %+v", host, got2)
		}
	}
}

func TestLossyExtensions_HermesPlatformFilter_IsNativeOnlyOnHermes(t *testing.T) {
	got, err := schema.LossyExtensions(resource.KindSkill, "hermes",
		map[string]any{"platforms": []any{"macos"}})
	if err != nil {
		t.Fatalf("LossyExtensions(hermes platforms on hermes): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no lossy reasons on hermes; got %+v", got)
	}

	got, err = schema.LossyExtensions(resource.KindSkill, "codex",
		map[string]any{"platforms": []any{"macos"}})
	if err != nil {
		t.Fatalf("LossyExtensions(hermes platforms on codex): %v", err)
	}
	if len(got) != 1 || got[0].CanonicalConcept != "hermes_skill_platform_filter" {
		t.Fatalf("expected hermes platform filter to be lossy on codex; got %+v", got)
	}
}

func TestLossyExtensions_MCPServerTypeMarker_NeverLossy(t *testing.T) {
	// schema/mcp-server.yaml documents `type` as pass-through metadata:
	// it is useful source annotation but not a load-bearing transport
	// discriminator on any host. The field must be bound via field_names
	// so lossy detection does not treat it as an unknown extension.
	for _, host := range []string{"claude-code", "gemini-cli", "codex", "made-up-host"} {
		got, err := schema.LossyExtensions(resource.KindMCPServer, host,
			map[string]any{"type": "stdio"})
		if err != nil {
			t.Fatalf("LossyExtensions(type) on %s: %v", host, err)
		}
		if len(got) != 0 {
			t.Errorf("host=%s type: expected no lossy (pass-through); got %+v", host, got)
		}
	}
}

func TestConcept_CanonicalisesTo_RoundTripsThroughLoad(t *testing.T) {
	// ADR-0017 Scenario B anchor: the schema parser must preserve
	// `canonicalises_to:` on Concept even when no current schema entry
	// uses it. Without this, a future schema author who adds Scenario B
	// would have their annotation silently dropped on parse.
	//
	// No production schema entry carries the field today, so this test
	// loads a synthetic Schema via YAML unmarshal (bypassing the
	// embed.FS) and asserts the field survives. If someone strips
	// CanonicalisesTo from the Concept struct as "unused", this fails.
	src := []byte(`kind: skill
deliberately_excluded:
  - canonical_concept: synthetic_scenario_b
    aliases:
      - host: future-host
        field_name: futureName
    canonicalises_to: url
`)
	var s schema.Schema
	if err := yamlUnmarshal(src, &s); err != nil {
		t.Fatalf("unmarshal synthetic schema: %v", err)
	}
	if len(s.DeliberatelyExcluded) != 1 {
		t.Fatalf("expected 1 concept; got %d", len(s.DeliberatelyExcluded))
	}
	if s.DeliberatelyExcluded[0].CanonicalisesTo != "url" {
		t.Errorf("CanonicalisesTo: got %q, want %q (Scenario B annotation must survive parse)",
			s.DeliberatelyExcluded[0].CanonicalisesTo, "url")
	}
}

func TestLossyExtensions_UnknownField_Lossy_UnknownConcept(t *testing.T) {
	// Per ADR-0012 §Why (failure-mode safety): an extension not listed
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

// TestLossyExtensions_CorporateDocumentationFields_NotLossy pins the
// pass-through concepts added for the ellarock-config corporate skill
// packages. Every field below is documentation: no surveyed host parses
// it, so installing a skill that carries it must not require
// --allow-lossy on any host. Before these entries existed, all 18 fields
// fell through to "unknown field" and made 8 of 90 skills permanently
// un-installable to claude-code without acknowledging data loss.
func TestLossyExtensions_CorporateDocumentationFields_NotLossy(t *testing.T) {
	fields := []string{
		// artifact_provenance_documentation
		"id", "artifact-type", "owner/surface", "purpose", "registered-in", "tests",
		// artifact_dataflow_documentation
		"inputs", "outputs", "state-read", "state-written", "failure-mode",
		// authorial_trigger_hints
		"triggers", "patterns",
		// authorial_environment_notes
		"permissions", "requires", "compatibility", "host-compatibility",
		// skill_config_schema_version
		"config-version",
	}
	// Checked on every adapter, not just claude-code: these are
	// pass-through everywhere, so no host may report them as lossy.
	hosts := []string{"claude-code", "gemini-cli", "antigravity-cli", "codex", "opencode", "hermes"}

	for _, host := range hosts {
		for _, field := range fields {
			got, err := schema.LossyExtensions(resource.KindSkill, host,
				map[string]any{field: "documentation value"})
			if err != nil {
				t.Fatalf("LossyExtensions(%s, %s): %v", host, field, err)
			}
			if len(got) != 0 {
				t.Errorf("field %q on host %s: expected pass-through, got lossy %+v",
					field, host, got)
			}
		}
	}
}

// TestLossyExtensions_UnknownFieldStillLossy is the control for the test
// above. Adding pass-through concepts must not degrade the ADR-0012 §Why
// default: a field no schema entry claims is still lossy everywhere.
// Without this, a change that made LossyExtensions return nothing at all
// would leave the previous test green.
func TestLossyExtensions_UnknownFieldStillLossy(t *testing.T) {
	got, err := schema.LossyExtensions(resource.KindSkill, "claude-code",
		map[string]any{"not-a-real-field": "x"})
	if err != nil {
		t.Fatalf("LossyExtensions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 lossy reason for an unclaimed field; got %+v", got)
	}
	if got[0].CanonicalConcept != "" {
		t.Errorf("unclaimed field should carry no concept; got %q", got[0].CanonicalConcept)
	}
}

// TestLossyExtensions_LoadBearingFieldsStayLossy guards the boundary the
// new entries sit next to. `permissions` is documentation and passes
// through; `allowed-tools` is the field Claude Code actually enforces and
// must stay lossy off-Claude. A future edit that swept load-bearing
// runtime fields into the pass-through buckets would fail here.
func TestLossyExtensions_LoadBearingFieldsStayLossy(t *testing.T) {
	for _, field := range []string{"allowed-tools", "platforms", "required_environment_variables"} {
		got, err := schema.LossyExtensions(resource.KindSkill, "opencode",
			map[string]any{field: "x"})
		if err != nil {
			t.Fatalf("LossyExtensions(%s): %v", field, err)
		}
		if len(got) != 1 {
			t.Errorf("load-bearing field %q must stay lossy off its host; got %+v", field, got)
		}
	}
}
