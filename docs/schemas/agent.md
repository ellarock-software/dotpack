# Agent Schema

**Version:** `0`

## Template

- **Shape:** `file_with_frontmatter`
- **Filename:** `<agent-name>.md`
- **Body:** `required`

## Fields

| Name | Type | Required | Notes |
| --- | --- | --- | --- |
| `name` | `string` | **Yes** | Unique identifier. Lowercase letters, digits, hyphens (Gemini spec also allows underscores; Claude convention is hyphens only). |
| `description` | `string` | **Yes** | Used by the host's dispatcher to decide when to invoke this agent. Two strong conventions in the corpus: "Use this agent when ..." (declarative trigger, VoltAgent style) and "Use PROACTIVELY for ..." (active-voice auto-invocation, 0xfurai/wshobson style). dotpack does not normalise; the translator passes through. |
| `model` | `string` | No | Despite 8/8 corpus presence, marked OPTIONAL because the documented Gemini CLI spec allows omission (defaults to inheriting the parent session's model). Corpus is Claude-biased — Claude conventionally sets model explicitly. Values seen: short alias ("opus", "sonnet", "haiku") and full model ID ("claude-sonnet-4-20250514"). Both are pass-through; adapters resolve per host. |
| `tools` | `string | array<string>` | No | All 5 Claude examples in corpus use a comma-separated STRING ("Read, Write, Edit, Bash, Glob, Grep") including MCP-prefixed names (mcp__context7__resolve-library-id). Gemini's documented spec uses a YAML ARRAY (["read_file", "grep_search"]) with snake_case tool names. dotpack accepts either; the translator normalises to array<string> internally and adapters re-emit the host-native shape. |

## Ecosystem Notes

- Agents live under `.claude/agents/*.md` (Claude Code), `.gemini/agents/*.md` (Gemini CLI), or `.antigravity/agents/*.md` (Antigravity CLI). The `.agents/agents/` convergence path is NOT yet honoured by either Claude or Gemini for the agent kind (unlike skills, which both honour `~/.agents/skills/`). agents-cli adapter will need to fan out to each host's native directory until convergence happens.
- Gemini's documented spec includes fields the Claude ecosystem does not
use in practice: `kind: local|remote`, `temperature`, `max_turns`
(snake_case), `timeout_mins`, inline `mcpServers`. None appeared in the
corpus. Treated as adapter-layer concerns per [ADR-0003](../adr/0003-universal-kinds-with-adapter-capability-matrix.md).
- Description-as-trigger is universal across hosts. The body becomes the agent's system prompt — markdown is the only format observed.
- Recurring body section conventions (Identity/Purpose, Capabilities, Approach, Workflow, Quality Checklist) are author conventions, NOT schema. dotpack does not enforce or rewrite body structure.

## Deliberately Excluded Concepts

### Concept: `claude_subagent_runtime_overrides`

Documented in the Claude Code subagent spec but absent from every
corpus example. These encode subagent runtime semantics:
conversation turn limits (`maxTurns`), tool denylist
(`disallowedTools`), permission gating (`permissionMode`), inline
subresource dispatch (`skills`, `mcpServers`, `hooks`, `memory`).
Grouped into one canonical_concept because corpus presence is 0/8
across the entire group; finer-grained concepts can be split out
if/when corpus evidence surfaces them. Adapter behaviour:
- claude-code adapter: emit natively when present.
- non-claude-code adapters: surface as lossy. These fields carry
  runtime semantics (especially `permissionMode` and `disallowedTools`,
  which are security-relevant) that other hosts cannot honour.
  Install proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `claude-code` | `maxTurns` |
| `claude-code` | `disallowedTools` |
| `claude-code` | `permissionMode` |
| `claude-code` | `skills` |
| `claude-code` | `mcpServers` |
| `claude-code` | `hooks` |
| `claude-code` | `memory` |

### Concept: `gemini_agent_runtime_overrides`

Gemini-spec-only fields, absent from corpus. These encode
Gemini-native agent runtime: agent locality (`kind: local|remote`,
naming clash with dotpack's universal `kind` taxonomy documented in
[ADR-0006](../adr/0006-agent-schema.md)), sampling parameter (`temperature`), conversation cap
(`max_turns` — snake_case, distinct from Claude's camelCase
`maxTurns`), wall-clock cap (`timeout_mins`). Adapter behaviour:
- gemini-cli adapter: emit natively when present.
- non-gemini adapters: surface as lossy. Install proceeds only
  with `--allow-lossy`.
Note: `max_turns` here is the Gemini name; Claude's `maxTurns` is
a separate canonical_concept (claude_subagent_runtime_overrides).
A future ADR-0017 may consolidate them under a single
conversation_turn_cap canonical_concept with both aliases — see
[ADR-0012](../adr/0012-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md) Consequences for the Scenario B deferral.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `gemini-cli` | `kind` |
| `antigravity-cli` | `kind` |
| `gemini-cli` | `temperature` |
| `antigravity-cli` | `temperature` |
| `gemini-cli` | `max_turns` |
| `antigravity-cli` | `max_turns` |
| `gemini-cli` | `timeout_mins` |
| `antigravity-cli` | `timeout_mins` |

### Body Section Normalisation

Survey proposed canonical body sections ("## Approach", "## Workflow",
etc.). Rejected — author conventions vary too much to force a single
layout, and the agent host doesn't parse section headers as
structure. Section recommendations live in the survey clusters
as ecosystem insight, not in the schema. NOT a field; no
canonical_concept; not a lossy-detection participant.

