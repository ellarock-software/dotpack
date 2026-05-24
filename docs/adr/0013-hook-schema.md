# Hook schema: nested fragment, config-merge installs, unit-divergence trap

## Context

Per [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md). Fifth kind surveyed. The `hook` kind is a CONFIG FRAGMENT — a slice of host configuration that gets merged into the host's settings file rather than dropped as a standalone file. The corpus is 4 Claude `.claude/settings.json` files + 1 Gemini `.gemini/settings.json` file. Codex hooks live in `~/.codex/config.toml` and were not surveyed (survey.sh TOML extraction is TODO).

## Decision

`schema/hook.yaml` is nested in three levels, not a flat field list:

1. **Top-level**: maps canonical event name (string) → list of bindings. Event names normalised across hosts (e.g., Claude's `PreToolUse` ≡ Gemini's `BeforeTool`).
2. **Binding**: `{ matcher, hooks: array<hook_spec> }`. `matcher` is a regex/glob; absent → "match everything".
3. **Hook-spec**: `{ type, command, [timeout] }`. `type` is always `"command"` in corpus. `timeout` is optional but **unit diverges** across hosts.

Deliberately excluded (1/5 corpus presence each): `async`, `once`, `statusMessage`, `name`, `description`.

## Why

**Nested structure, not flat fields.** The survey produced a flat list of fields (`on`, `type`, `command`, `matcher`, `timeout`, ...). Empirically wrong shape — `matcher` is at the binding level while `type`/`command` are at the hook-spec level. A flat schema would let a translator output `{ matcher: "...", type: "command", command: "..." }` as a single object, which doesn't match what either host parses. Reshaping the schema to mirror the actual JSON structure costs nothing (still empirically derivable) and prevents the translator from emitting invalid configs.

**`type` is effectively a constant.** All 5 examples have `type: "command"`. Documented host specs mention other types may exist but none observed. Keeping it as required because (a) hosts gate on it, (b) future types (e.g., `inline`, `mcp-tool`) may emerge and dotpack should not assume.

**The timeout unit trap.** Claude's hooks specify timeout in **seconds** (corpus: 5, 30, 60, 90). Gemini's specifies timeout in **milliseconds** (corpus: 8000 = 8 seconds). A naive cross-host translation that copies `timeout: 8` from Claude to Gemini reduces a 8-second budget to 8 milliseconds. This is a real silent semantic bug. The schema documents the divergence; adapters declare the unit they emit; the translator must convert. This is the single most important note in this ADR — it would not have been visible without including both hosts in the corpus.

**Event-name normalisation.** Claude's `PreToolUse` ≡ Gemini's `BeforeTool`. The translator maps these. dotpack's canonical names use Claude's PascalCase convention because (a) more corpus presence (4/5), (b) Gemini's naming is descriptive but no more canonical, (c) Claude's includes more event types (Session*, Subagent*, PermissionRequest) with no Gemini equivalent — picking Gemini's names would force re-coining. Adapters re-emit in host-native form.

**`enabled` registry kept as adapter concern.** Gemini's example 5 has a top-level `enabled: ["strict-any", "lint-fix", ...]` that selectively activates named hooks. Claude has no equivalent. Universal kind doesn't adopt — the Gemini adapter handles it when emitting (matches up `name` field on hook-spec to the `enabled` list). The Gemini-only `name` and `description` fields on hook-spec serve this registry; they're not adopted universally for the same reason.

**Methodology — floor rule held this time.** Survey output marked `async`, `once`, `statusMessage`, `name`, `description` with hand-wavy installation-failure rationales ("would prevent non-blocking hooks from functioning"). We applied the floor rule strictly: omitting `async` defaults to sync (functional, not broken), so it's not an installation-blocker. All 1/5 fields excluded. This is the third kind where the survey agent overreached on `required: true` despite the tightened prompt; the methodology lesson is documented but the survey output is corrected by hand.

## Consequences

**This is the first kind installed via config-merge** rather than file drop. Per [ADR-0008](./0008-manifest-as-install-provenance-source-of-truth.md), the manifest must track `merged_keys` for hook installs — exact set of paths inside the target settings.json (e.g., `$.hooks.PreToolUse[3]`) that this hook resource added — so `uninstall` can surgically remove them without disturbing user-added or other-resource-added entries. This is task #4 territory and was anticipated by ADR-0008's `merged_keys: [...]` field.

**Codex hook support is blocked on TOML extraction.** Adding Codex to the corpus requires fixing `scripts/survey.sh` line ~88-91 (TOML fragment extraction marked TODO). Not in scope for this ADR; tracked. Until then, the Codex side of the `agents-cli` adapter declares `hook = unsupported` in its capability matrix; users targeting Codex see a clear refusal per ADR-0007's default-deny stance.

**The `agents-cli` fan-out problem hit again.** `agents-cli` wants to install hooks into both `.gemini/settings.json` AND `~/.codex/config.toml` (when Codex hooks land). These are different files in different formats — JSON vs TOML, different event names, different timeout units. Task #4 (agents-cli adapter fan-out design) now has three things to resolve: skill/agent/command file placement, hook config merge across two formats, and mcp-server config merge (next ADR).

**Adapter capability matrix entries.**
- `claude-code`: native. Merges into `.claude/settings.json $.hooks`. Timeout in seconds. Manifest tracks merged keys.
- `agents-cli/gemini`: native. Merges into `.gemini/settings.json $.hooks`. Timeout in milliseconds. `enabled` registry handling.
- `agents-cli/codex`: unsupported pending TOML extraction. Default-deny.

## Artefacts

- `schema/hook.yaml` (nested structure documented)
- `schema-corpus.yaml` (kind: hook) — 4 Claude JSON + 1 Gemini JSON
- `.dotpack-workdirs/survey/hook/`
