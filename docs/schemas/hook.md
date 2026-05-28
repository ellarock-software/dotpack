# Hook Schema

**Version:** `0`

## Template

- **Shape:** `config_fragment`
- **Filename:** ``

## Ecosystem Notes

- Event naming convergence: Claude and Codex use identical PascalCase event names (PreToolUse, PostToolUse, SessionStart, UserPromptSubmit, Stop, PermissionRequest, ...). Gemini uses BeforeTool / AfterTool. Translator event-mapping table is two-way (Claude/Codex ↔ Gemini), not three-way. Significant translator simplification vs the original schema's three-way assumption.
- Timeout unit divergence has TWO sides, not three: Claude=seconds, Codex=seconds, Gemini=milliseconds. Adapter capability matrix must declare which unit it emits and the translator must convert.
- Timeout corpus anomaly: shanraisshan/claude-code-best-practice (.claude/settings.json) has timeout values 5000 and 30000 in a field Claude treats as seconds — almost certainly the author confused units. Their hooks effectively never time out (5000 seconds = 83 minutes). Translator should warn when a Claude/Codex timeout exceeds a sane upper bound (e.g., 600 seconds) to catch this user error class.
- PostToolUseFailure is in 3 of 7 files (2 Claude + 1 Codex) but is NOT in the published Codex spec event-name table. Treat as canonical based on cross-host adoption; flag in adapter docs that Codex parser behavior on it is undocumented.
- In-the-wild non-spec TOML shapes (NOT in this corpus): flat array-of-tables `[[hooks]] event = '...' command = '...'` (seen at alinaqi/maggy) and flat key-value map `[hooks] PreToolUse = '...'` (seen at vbcherepanov/total-agent-memory). These may not actually install on current Codex — the spec only documents the nested form surveyed here. Translator should accept them as IMPORTS for legacy interop but EMIT the canonical nested form.
- Gemini's current settings schema stores hook bindings under the top-level `hooks` object (`hooks.BeforeTool`, `hooks.AfterTool`, ...), with `hooksConfig.enabled` as a global boolean and `hooksConfig.disabled` as the per-name disable list. The older corpus file used a top-level `enabled: [hook-name, ...]` registry; dotpack does NOT emit that legacy registry. The Gemini-only `name` field on hook-spec remains useful for logs and disabled-list targeting, but is not adopted universally.

## Deliberately Excluded Concepts

### Concept: `gemini_hook_execution_mode`

Gemini-specific execution-mode flags: `async` toggles non-blocking
hook execution (default: sync), `once` toggles single-fire-per-session
(default: every-trigger). The omission-failure rationale (Deviation
2 of [ADR-0011](../adr/0011-floor-rule-deviation-criterion.md)) doesn't hold — both defaults are functional, just
different from the explicit-true behaviour. Below floor. Adapter
behaviour:
- gemini-cli adapter: emit natively when present.
- non-gemini adapters: surface as lossy. Although the defaults are
  functional, an author who explicitly set `async: true` chose
  non-blocking execution; silently emitting a blocking hook elsewhere
  changes runtime characteristics. Install proceeds only with
  `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `gemini-cli` | `async` |
| `antigravity-cli` | `async` |
| `gemini-cli` | `once` |
| `antigravity-cli` | `once` |

### Concept: `gemini_hook_registry_identifiers`

Gemini-specific identifying fields used in hook logs and by
Gemini's `hooksConfig.disabled` list. Claude and Codex have no
equivalent hook-name control surface; bindings are active when
present. Adapter behaviour:
- gemini-cli adapter: emit natively when present. When absent,
  the adapter may synthesize a stable name from the dotpack hook
  resource name so Gemini users can later target it in
  `hooksConfig.disabled`.
- non-gemini adapters: drop silently. These fields are pure
  identification metadata with no runtime effect on hosts that
  don't parse hook names. NOT lossy (lossy_when_dropped: false) —
  dropping them on Claude/Codex does not change install behaviour.
This is the canonical example for the `lossy_when_dropped: false`
escape hatch introduced in [ADR-0012](../adr/0012-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md) §8. If a future host introduces
a hook-name control concept and would parse these fields, add it to
`aliases` and flip lossy_when_dropped to true.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `gemini-cli` | `name` |
| `antigravity-cli` | `name` |
| `gemini-cli` | `description` |
| `antigravity-cli` | `description` |

