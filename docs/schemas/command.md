# Command Schema

**Version:** `0`

## Template

- **Shape:** `file_with_prompt_or_body`
- **Filename:** ``

## Fields

| Name | Type | Required | Notes |
| --- | --- | --- | --- |
| `prompt` | `string` | **Yes** | The instruction text the LLM executes. In .md files, this is the markdown body after frontmatter. In .toml files, this is an explicit `prompt = "..."` field. dotpack treats them as the same canonical field; adapters re-emit native form. |
| `description` | `string` | No | Brief one-line summary. Present in the anthropics example and all three Gemini TOML examples; absent in wshobson's model-only commands. Used by host `/help` menus. |
| `model` | `string` | No | Per-command model selection. Values seen: full model ID ("claude-sonnet-4-0", "claude-opus-4-1") — wshobson uses full IDs. Other corpora may use aliases (sonnet/opus/haiku). Adapter resolves per host; Gemini commands inherit session model and have no native slot for this. |
| `allowed-tools` | `string | array<string>` | No | KEPT despite below-floor (1/7) because absence of this field changes runtime semantics in Claude Code (without it, every Bash invocation prompts for approval; with it, listed tools are auto-approved). The anthropics official example uses "Bash(git add:*), Bash(git commit:*)" filter syntax. Per [ADR-0003](../adr/0003-universal-kinds-with-adapter-capability-matrix.md), this is technically adapter-layer (runtime permissions), but is retained here because Claude's documented spec includes it and it has corpus presence. Adapters that don't support it warn at install time. |

## Ecosystem Notes

- Two formats, one concept. Claude commands are markdown + optional YAML frontmatter; Gemini commands are TOML files containing a `prompt` field. The translator unifies on the dotpack representation; adapters re-emit the host-native shape on install.
- Argument placeholders diverge: Claude uses $ARGUMENTS (and $1, $2 positional); Gemini uses {{arg0}} double-curly templating. The translator handles substitution syntax during cross-format port; the schema does not enforce one.
- Filename → command-name mapping: both hosts derive the command name from the filename (minus extension). Subdirectories become namespaces: Claude uses subdir prefixes; Gemini uses colons (`agents/start.toml` becomes `/agents:start`).

## Deliberately Excluded Concepts

### Concept: `claude_command_runtime_overrides`

Documented in the Claude Code command frontmatter reference but
absent from corpus. These encode Claude-native command ergonomics:
CLI argument hinting (`argument-hint`) and invocation gating
(`disable-model-invocation`, which prevents the model from
auto-triggering the command). Grouped into one canonical_concept
because corpus presence is 0/7 across the group. Adapter behaviour:
- claude-code adapter: emit natively when present.
- non-claude-code adapters: surface as lossy. `disable-model-invocation`
  is particularly load-bearing — silently dropping it on a host that
  lacks the concept would change runtime invocation semantics.
  Install proceeds only with `--allow-lossy`.
Note: the universal `allowed-tools` field is PROMOTED to the schema
(not deliberately_excluded) under the Deviation 2 rule of [ADR-0011](../adr/0011-floor-rule-deviation-criterion.md)
— see the field's notes block above. It is NOT a lossy-detection
concern; adapters that don't support it warn at install time
rather than block.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `claude-code` | `argument-hint` |
| `claude-code` | `disable-model-invocation` |

### Body Section Normalisation

Survey clusters.md noted recurring body sections in wshobson
Markdown commands (## Context, ## Requirements, ## Instructions).
Not adopted — these are author conventions for prompt structure,
not parseable schema. Same posture as [ADR-0006](../adr/0006-agent-schema.md) for agent body.
NOT a field; no canonical_concept; not a lossy-detection
participant.

