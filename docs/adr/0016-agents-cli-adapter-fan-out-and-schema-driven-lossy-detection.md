# agents-cli adapter fan-out, canonical-form emit, schema-driven per-instance lossy detection

## Context

The `agents-cli` adapter is the first one that writes to multiple host config files. For the four file-drop kinds (skill, agent, command, memory) the convergence story holds: both Gemini CLI and Codex CLI honour `.agents/` and `~/.agents/`, so the install is one filesystem write. For the two config-fragment kinds (hook, mcp-server) the hosts diverge — Gemini reads `.gemini/settings.json`, Codex reads `~/.codex/config.toml`, and these files differ in format (JSON vs TOML), key names (camelCase vs snake_case), event names (Gemini-specific `BeforeTool`/`AfterTool` vs PascalCase elsewhere), timeout units (Gemini ms vs Claude/Codex seconds), and supported fields (Codex's mcp-server schema is a ~18-field superset, see [ADR-0014](./0014-mcp-server-schema.md)).

Settling this exposed seven follow-on questions that all sit inside the same architectural choice: adapter granularity, canonical-form representation, format-conversion mechanism, key-name and event-name canonicalisation, timeout unit conversion, mcp-server discriminated transport, manifest `merged_keys` tracking, and the per-instance lossy detection promised by [ADR-0007](./0007-universal-kinds-with-adapter-capability-matrix.md)'s 2026-05-24 addendum. ADR-0007 picked adapter-declared `lossy_when_fields: [...]` but that decision predates the Codex re-survey ([ADR-0013](./0013-hook-schema.md), [ADR-0014](./0014-mcp-server-schema.md)), which made the duplication cost visible: ~23 field names restated per non-Codex adapter, multiplied by adapter count, drifting independently as the corpus grows.

## Decision

### 1. Adapter granularity — two sub-adapters, one CLI flag

`gemini` and `codex` are independent `Adapter` implementations, each with its own per-host kind-support data (`Policy.Layouts` membership for the file-drop adapter; see §2 + [ADR-0007](./0007-universal-kinds-with-adapter-capability-matrix.md)). `--agent agents-cli` is an orchestrator-side CLI flag that resolves to the adapter set `{gemini, codex}`. The umbrella CLI flag does not become a third adapter. For file-drop kinds the orchestrator special-cases the flag to write `.agents/` once rather than invoking both sub-adapters with redundant writes; for config-fragment kinds the orchestrator invokes each sub-adapter and aggregates their install plans.

### 2. Adapter interface — plan-returning, not filesystem-mutating

```go
type Adapter interface {
    HostID() string                                          // "claude-code", "gemini-cli", "codex"
    Plan(resource Resource, scope Scope) (InstallPlan, error)
}

type InstallPlan struct {
    Files      []FileWrite      // path + content for file-drop kinds
    MergedKeys []MergedKeyWrite // (file, json_path or toml_path, value) for fragment kinds
    TargetDir  string           // dir the orchestrator may reclaim on uninstall when empty
}

type LossyReason struct { // populated by the orchestrator, not the adapter
    FieldPath        string
    CanonicalConcept string
    SupportedHosts   []string
}
```

Per-kind support is expressed by `Plan`'s behaviour: unsupported kinds return a typed error (`"<host>: kind <k> not yet supported"`); supported kinds return an `InstallPlan`. For the file-drop adapter, `Policy.Layouts` membership is the data structure that drives this — a kind present in `Layouts` is supported; a kind absent yields the error. There is no separate `Capabilities()` interface method: the architecture review (cards #1, #5) collapsed the old per-(kind, adapter) `Native`/`Lossy`/`Unsupported` matrix into this Plan-error contract because no production caller traversed the queryable form (see [ADR-0007](./0007-universal-kinds-with-adapter-capability-matrix.md) for the rationale and history).

Per-instance lossiness is computed by the orchestrator after `Plan` returns, using §8's schema-driven check against the resource's `Extensions` and the adapter's `HostID`. The plan does NOT carry lossy state — keeping a `plan.Lossy` field invited adapters to restate schema knowledge in code, which §8 explicitly supersedes. The orchestrator's policy is: `Plan` errors with "kind not supported" → refuse; per-instance lossy reasons present AND `--allow-lossy` not set → refuse with `LossyError`; otherwise proceed.

Adapters are pure functions of the resource. The orchestrator applies plans, persists manifest records, and gates lossy plans behind `--allow-lossy`. This keeps adapters trivially unit-testable, makes dry-run free (`Plan` without `Apply`), and centralises manifest construction.

### 3. Canonical-form internal representation — typed Go structs per kind

`internal/resource/` declares one struct type per kind, mirroring the schema. The validator produces a `Resource` interface value (one of `*Skill`, `*Agent`, `*Command`, `*Memory`, `*Hook`, `*MCPServer`); adapters consume it. Per-kind logic is the norm rather than a generic walker (already noted in the schema files — kind shapes are non-uniform).

Extension fields not in the universal core (e.g., Codex's `default_tools_approval_mode`) are preserved on a per-kind `Extensions map[string]any` so the per-instance lossy check has data to inspect. Adapters that natively support an extension copy it into their emit; adapters that don't surface it as a `LossyReason`.

### 4. Format conversion — Go libraries in production, yq is survey-only

Production binary uses `encoding/json` (stdlib) and `github.com/pelletier/go-toml/v2` for TOML emit. The `yq -p toml -o json` pattern in `scripts/survey.sh` is survey-time only — the production binary does not shell out for hot-path format conversion. Adding `pelletier/go-toml/v2` is the only new direct dependency this ADR sanctions.

### 5. Key-name and event-name canonicalisation — schema-driven, emit-time

The canonical in-memory form uses one chosen name per concept: `mcpServers` (camelCase, matches 2 of 3 hosts including the .mcp.json file format that anchors the kind), PascalCase event names (`PreToolUse`, `PostToolUse`, etc., identity on Claude+Codex per [ADR-0013](./0013-hook-schema.md)). Per-host emit rewrites are declared in the schema's existing tables:

- `template.source_locations[]` declares `file`, `json_path` or `toml_path`, plus the per-host key name (e.g., `mcp_servers` for codex).
- `cross_ecosystem_event_aliases[]` in `schema/hook.yaml` declares the Gemini event-name map.

Adapters consult the schema; they do not carry duplicate copies of mapping tables.

### 6. Timeout unit conversion — canonical seconds, Gemini multiplies on emit

The canonical `timeout` field carries integer seconds. The Gemini adapter multiplies by 1000 on emit. The validator emits a warning when canonical `timeout > 600` to catch the unit-confusion class documented in [ADR-0013](./0013-hook-schema.md) (shanraisshan corpus anomaly: 5000 and 30000 in a Claude-seconds field, almost certainly user error).

### 7. mcp-server discriminated transport — field presence, not adapter concern

Per [ADR-0014](./0014-mcp-server-schema.md), transport is `command/args` (stdio) XOR `url` (HTTP), discriminated by field presence. This is a **validator** invariant, not an adapter responsibility — adapters emit whichever transport fields are present on a validated resource. The validator's machine-readable `oneOf` / `discriminator` semantics is deferred to MVP implementation (#5).

### 8. Per-instance lossy detection — schema-declared `aliases: [{host, field_name}, ...]`

This supersedes the adapter-declared `lossy_when_fields` decision in [ADR-0007](./0007-universal-kinds-with-adapter-capability-matrix.md)'s 2026-05-24 addendum.

Each `deliberately_excluded` entry in a schema gains a `canonical_concept` slug, an `aliases` object array, and an optional `lossy_when_dropped` boolean (default `true`). Optional `canonicalises_to:` names a universal-core field that the concept is equivalent to (for Scenario B deferral — see Consequences). Example (mcp-server):

```yaml
deliberately_excluded:
  - canonical_concept: per_tool_approval
    aliases:
      - host: codex
        field_name: "tools.<id>.approval_mode"
    # lossy_when_dropped defaults to true — dropping a security control is lossy
    reason: |
      ...
  - canonical_concept: stdio_working_dir
    aliases:
      - host: gemini-cli
        field_name: cwd
      - host: codex
        field_name: cwd
    reason: |
      ...
  - canonical_concept: transport_type_marker
    aliases: []                          # no host parses semantically
    lossy_when_dropped: false            # pass-through metadata; dropping is a no-op
    reason: |
      ...
  - canonical_concept: gemini_http_url_alias
    aliases:
      - host: gemini-cli
        field_name: httpUrl
    canonicalises_to: url                # Scenario B: same concept as universal `url`
    reason: |
      ...
```

Lossy-detection algorithm (orchestrator-side, after `Plan` returns): walk the resource's `Extensions`; for each present field, find the matching `canonical_concept` in the kind's schema. If `lossy_when_dropped: false`, skip — this concept is pass-through metadata (`name`/`description` for Gemini's `enabled` registry, `transport_type_marker` for mcp-server, `discovery_keywords` for skill, etc.). Otherwise, the set of supporting hosts is `aliases[].host`; if the install's target host is not in that set, append a `LossyReason`. Aggregate across all sub-adapters when a CLI flag fans out (e.g., `--agent agents-cli` requires `--allow-lossy` if **any** sub-adapter would drop **any** field whose concept is `lossy_when_dropped: true`).

The `lossy_when_dropped: false` escape hatch exists because some host-specific fields encode bookkeeping or aliases with no runtime effect elsewhere: dropping them on a non-supporting host changes nothing observable. Requiring `--allow-lossy` for those would be over-strict friction with no safety upside. The default stays `true` because the safer-failure-mode argument (silent semantic drop is worse than loud over-strict block) holds for everything semantically load-bearing — the override must be explicit and justified per-entry.

`canonicalises_to:` is a Scenario B anchor — when the universal core promotes one host's name (e.g., `url` from Codex) but another host uses a different name for the same concept (e.g., `httpUrl` on Gemini), the alias entry annotates the canonical field it folds into. For MVP, the translator is expected to canonicalise on import; if it fails to, lossy detection over-fires (the resource carries the host-specific name and the target host isn't in `aliases`). The annotation makes the eventual ADR-0017 alias-aware translation mechanical rather than archaeological.

The object-array form (not a map keyed by host) is chosen because (a) it matches the schema's existing convention in `template.source_locations`, (b) it nudges Go code toward data-driven iteration (`for _, alias := range aliases`) rather than baked-in host enums, and (c) it extends per-host without restructuring (a future `deprecated_alias: ["headers"]` annotation on a Codex entry is a field-add, not a shape change).

A separate per-field `host_support: [...]` projection is not stored — it would be redundant with `aliases[].host` and create drift risk. Adapters that need the projection compute it from `aliases`.

### 9. Manifest `merged_keys` — populated from the install plan

Per [ADR-0008](./0008-manifest-as-install-provenance-source-of-truth.md). Adapter plans carry `MergedKeys`; the orchestrator persists them into `~/.dotpack/installs.yaml`. Multiple keys per hook install (one per binding leaf path, e.g., `$.hooks.PreToolUse[3]`), one per mcp-server install (`$.mcpServers.<name>` or `mcp_servers.<name>` per host).

### 10. Schema as adapter contract — comprehensive inline documentation required

The architecture above makes the schema the single source of truth for adapter behaviour: source locations, key names, event aliases, transport discrimination, canonical concepts, per-host aliases, deliberate exclusions. Adapters are mechanical consumers; the schema is what carries the design intent.

Therefore every schema entry that an adapter or future contributor (human or AI) might consume must carry inline documentation sufficient to extend it without out-of-band knowledge:

- Every field in `structure:` or `server_entry_fields:` already has `notes:` — keep this discipline.
- Every `deliberately_excluded` entry must have a `canonical_concept` slug, an `aliases` array, and a `reason:` block stating (i) what semantics the field encodes, (ii) why it's not in the universal core, (iii) which hosts support the concept and under what native names, (iv) what an adapter should do when it encounters it (emit natively, surface as lossy, drop silently with warning).
- Every `cross_ecosystem_event_aliases` or similar mapping table must document the canonical name choice, the alias direction, and what triggers a warning during conversion.
- Every `ecosystem_notes` bullet must specify whether it's a translator concern, adapter concern, validator concern, or security-agent concern — the same observation triggers different code in different stages.

This bar exists because the survey LLM has shape-regressed on four of six kinds during Phase 0 ([ADR-0013](./0013-hook-schema.md) methodology note); without inline documentation as the authoritative spec, the regressed shape would look as plausible as the correct one. The schema is the contract, and the contract is read by AI agents at every re-survey and extension.

## Why

- **Two sub-adapters over one umbrella.** Per-host asymmetry (a Codex-rich mcp-server is native on `codex`, lossy on `gemini`) is real and the single-row collapse hides it. The umbrella special-case at the orchestrator handles file-drop convergence without forcing the adapter abstraction to lie.
- **Plan-returning interface.** Testability, dry-run, manifest construction all become trivial. The cost (one extra indirection) is uniform across all adapters.
- **Typed structs over generic maps.** Per-kind shape divergence (already documented in every schema file) means a generic walker mostly does conditional-branching on kind anyway; typed structs make this branching explicit and let the Go compiler enforce the per-kind contract.
- **Go libraries over yq.** Subprocess-shelling in the install hot path is fragile (PATH dependencies, version skew, error handling). `pelletier/go-toml/v2` is the mature pure-Go choice and adds one direct dependency.
- **Schema-declared aliases over adapter-declared lossy lists.** Three asymmetric arguments:
  1. **Failure-mode safety.** Forgetting to update an adapter-declared list when the corpus grows produces *silent* lossy-install failures — exactly what dotpack exists to prevent. Forgetting to update a schema alias produces an *over-strict* block requiring `--allow-lossy` — loud and recoverable.
  2. **Single source of truth.** The schema already enumerates excluded fields with reasons; restating them in adapter code is duplicate knowledge that drifts.
  3. **New-adapter onboarding cost.** Under adapter-declared lists, every new adapter has to discover and restate the cross-host superset. Under schema-declared aliases, a new adapter declares its `HostID` and inherits lossy detection.
- **Object-array aliases over host-keyed map.** Consistency with `source_locations`, runtime-data over compile-time host enums, per-host extensibility.
- **Canonical = seconds + PascalCase + mcpServers.** Matches the majority of hosts and the spec-canonical forms. Minimises the surface area of per-host rewrites (Gemini-only for both event names and timeout unit).
- **Inline schema documentation as a hard requirement.** Phase 0 produced empirical evidence (shape regressions on four of six kinds) that the schema, not the methodology, is what survives across re-surveys. The methodology can drift; the schema is what's read every time.

## Consequences

- **[ADR-0007](./0007-universal-kinds-with-adapter-capability-matrix.md) 2026-05-24 addendum partially superseded.** The sentence "MVP needs adapter-side `lossy_when_fields: [...]` declarations" is replaced by this ADR's schema-declared `aliases` mechanism. The rest of the addendum (per-resource install-time check, default-deny without `--allow-lossy`) stands. ADR-0007 to be updated with a back-reference.
- **Schema files need a one-shot pass to add `canonical_concept` + `aliases` to existing `deliberately_excluded` entries.** Mechanical work; falls into #5 (MVP implementation). The current entries already encode the data informally in `reason:` and `ecosystem_notes`; this is restructuring, not new survey work.
- **Cross-host concept-equivalence under different names (Scenario B from the design conversation) is not handled here.** Today's only ambiguous case (`headers` vs `http_headers`) is intra-Codex and translator-handled. If a future kind exhibits a real cross-host alias case (e.g., Cursor adopting per-tool approval under a different name from Codex's `tools.<id>.approval_mode`), the current `aliases` object array already accommodates it — but the *translator* would need an additional concept-equivalence rewrite step at import time. Deferred to ADR-0017 if/when the corpus surfaces a concrete case.
- **Orchestrator gains a CLI-flag-to-adapter-set alias table.** `agents-cli → {gemini, codex}` is the first entry; future flags (`all`, `cursor-ish`) live in the same table. The flag set is small enough to keep as a Go map; not a config file.
- **One new direct dependency.** `github.com/pelletier/go-toml/v2`. No transitive churn.
- **The schema is now the adapter contract.** Future schema changes are adapter-affecting changes; the survey methodology in [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md) implicitly assumed the schema was only the validator's input. ADR-0003 should be updated to reflect the broader role.
- **No test architecture is settled here.** Per the task-list note: test architecture is discovered TDD-style during #5, not pre-designed.

## Artefacts

- This ADR
- Updates pending: [ADR-0007](./0007-universal-kinds-with-adapter-capability-matrix.md) (back-reference + addendum sentence supersession), [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md) (schema-as-contract role)
- Schema updates pending in #5: `aliases` + `canonical_concept` on every existing `deliberately_excluded` entry across the six kind schemas
- Code pending in #5: `internal/adapter/`, `internal/resource/`, orchestrator's plan-apply + manifest persistence, per-instance lossy detection
