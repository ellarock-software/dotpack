# Hook schema: nested fragment, config-merge installs, unit-divergence trap

## Context

Per [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md). Fifth kind surveyed; **re-surveyed with Codex corpus after `scripts/survey.sh` TOML extraction landed** (task #14). Corpus is now 4 Claude `.claude/settings.json` + 1 Gemini `.gemini/settings.json` + 2 Codex `.codex/config.toml` = 7 files, 66 bindings, 67 hook-spec leaves.

The `hook` kind is a CONFIG FRAGMENT — a slice of host configuration that gets merged into the host's settings file rather than dropped as a standalone file.

## Decision

`schema/hook.yaml` is nested in three levels, not a flat field list:

1. **Top-level**: maps canonical event name (string) → list of bindings. Event names normalised across hosts.
2. **Binding**: `{ matcher, hooks: array<hook_spec> }`. `matcher` is a regex/glob; absent → "match everything".
3. **Hook-spec**: `{ type, command, [timeout], [statusMessage], [env] }`. `type` is always `"command"` in corpus. `timeout` unit diverges across hosts.

## Why

**Claude/Codex are an identity mapping on event names; only Gemini needs translation.** Codex CLI uses Claude's PascalCase event names verbatim: `PreToolUse`, `PostToolUse`, `SessionStart`, `UserPromptSubmit`, `Stop`, `PermissionRequest`, `PostToolUseFailure`. This collapses the translator's event-mapping table from three-way to **two-way**: Claude/Codex ↔ Gemini (`PreToolUse ↔ BeforeTool`, `PostToolUse ↔ AfterTool`). The original ADR assumed three-way mapping because Codex was unsurveyed; that assumption was wrong and is now retired. Real architectural simplification, not just a finding.

**Nested structure, not flat fields.** The re-survey produced a flat field list (`on`, `type`, `command`, `matcher`, `timeout`, `statusMessage`) — same shape error the original survey made. Empirically wrong: `matcher` is a binding-level field, `type`/`command` are hook-spec-level. The flat shape would let a translator emit `{ matcher, type, command }` as a single object, which neither host parses. Hand-verified counts at the right level (66 bindings, 67 leaves) and reshaped the schema to mirror the actual JSON structure. Holds for Codex too — arc-kit and oacb-strict use the spec-conforming nested form.

**`type` is effectively a constant.** All 67 hook-spec leaves have `type: "command"`. Codex spec explicitly says prompt and agent hook handlers are "parsed but skipped" — only command works. Kept required for forward-compatibility.

**The timeout-unit trap is two-sided, not three-sided.** Original ADR identified Claude=seconds vs Gemini=milliseconds as the divergence. With Codex corpus: Codex hooks also use **seconds** (arc-kit `timeout = 10`, oacb-strict `timeout = 3, 5` — clearly seconds, would be 3-10ms which is nonsense for script execution). The split is Claude+Codex (seconds) vs Gemini (milliseconds). Translator's unit map is now Gemini↔Claude/Codex, halving the conversion logic.

**Corpus anomaly worth a translator warning.** `shanraisshan/claude-code-best-practice/.claude/settings.json` has timeout values 5000 and 30000 in a field Claude treats as seconds. Almost certainly the author confused units (these hooks effectively never time out — 5000 seconds = 83 minutes). The translator should warn when a Claude/Codex timeout exceeds a reasonable upper bound (suggested: 600 seconds) to catch this user-error class. Documented in `schema/hook.yaml` ecosystem_notes.

**`statusMessage` and `env` promoted to optional hook_spec fields.** Original schema kept both as deliberately-excluded (1/5 corpus presence each). Updated corpus: `statusMessage` at 33/67 leaves (49% across Claude + Gemini + Codex), `env` at 5/67 leaves (single-author concentration in oacb-strict, but spec-sanctioned and security-relevant). Both above the floor; `env` retained on spec-evidence grounds despite single-file corpus.

**`PostToolUseFailure` added to canonical events even though it's not in the Codex spec table.** 3/7 files (2 Claude + 1 Codex) use it. Cross-host adoption clears the floor; adapter docs should flag that Codex parser behavior on this event name is undocumented (may or may not fire).

**Methodology — floor rule held, BUT shape regression caught only by hand.** Survey agent flattened the nested structure to a 6-field list, same failure mode as the original survey of this kind. The methodology lesson stands: nested-structure kinds (hook, mcp-server with deep tools.<id> nesting) require human reshape after the survey. The agent's per-leaf counts were close to correct (off by one — 66 vs 67) but useless without the structure.

## Consequences

**This is the first kind installed via config-merge** rather than file drop. Per [ADR-0008](./0008-manifest-as-install-provenance-source-of-truth.md), the manifest must track `merged_keys` for hook installs — exact set of paths inside the target settings file (e.g., `$.hooks.PreToolUse[3]`) that this hook resource added — so `uninstall` can surgically remove them without disturbing user-added or other-resource-added entries.

**`agents-cli` adapter fan-out (task #4) now has Codex specifics.** `agents-cli` installs hooks into BOTH `.gemini/settings.json` (JSON, BeforeTool/AfterTool, ms) AND `~/.codex/config.toml` (TOML, PreToolUse/PostToolUse, seconds). Per-host emit logic needed; Codex no longer the `unsupported` outlier.

**Adapter capability matrix entries.**
- `claude-code`: native. Merges into `.claude/settings.json $.hooks`. Timeout in seconds. Manifest tracks merged keys.
- `agents-cli/gemini`: native. Merges into `.gemini/settings.json $.hooks`. Timeout in milliseconds. `enabled` registry handling.
- `agents-cli/codex`: **native (upgraded from unsupported)**. Merges into `~/.codex/config.toml hooks`. Timeout in seconds. Same nested matcher-group shape as Claude. PostToolUseFailure may be silently dropped by Codex parser — adapter docs flag.

## Artefacts

- `schema/hook.yaml` (nested structure + Codex evidence)
- `schema-corpus.yaml` (kind: hook) — 4 Claude JSON + 1 Gemini JSON + 2 Codex TOML
- `scripts/survey.sh` — TOML extraction via `yq -p toml -o json` (task #14 landed)
- `.dotpack-workdirs/survey/hook/`
