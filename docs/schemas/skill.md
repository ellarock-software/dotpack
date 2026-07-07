# Skill Schema

**Version:** `0`

## Template

- **Shape:** `file_with_frontmatter`
- **Filename:** `SKILL.md`
- **Body:** `required`

## Fields

| Name | Type | Required | Notes |
| --- | --- | --- | --- |
| `name` | `string` | **Yes** | Unique identifier. Letters, numbers, and hyphens only per Gemini CLI spec (other ecosystems converge on the same constraint). |
| `description` | `string` | **Yes** | The primary LLM-triggering field. Two common phrasings in the wild: "Use when ..." (declarative trigger) and "Use this skill whenever ..." (imperative trigger). Both work; dotpack does not normalise. Recommend <= 1024 chars (Anthropic convention noted in body of obra/writing-skills and anthropic/skill-creator). |
| `license` | `string` | No | Anthropic-ecosystem convention (present in 3/4 anthropics/skills examples, absent everywhere else in the corpus). Free-form pointer to LICENSE.txt or a literal SPDX/proprietary string. Pass-through during translation. |

## Ecosystem Notes

- SKILL.md is the canonical filename across Claude Code, Gemini CLI, Antigravity CLI, Codex CLI, OpenCode, and Hermes. The `~/.agents/skills/` alias is honoured by Gemini and Codex; Hermes documents `~/.hermes/skills/` as the primary skill root plus `skills.external_dirs` in `~/.hermes/config.yaml` for extra search paths such as `~/.agents/skills/`. The on-disk package layout (folder with SKILL.md + optional scripts/, references/, assets/) is identical across these hosts.
- dotpack copies regular support files under the source skill directory (for example `scripts/*`, `references/*`, and `assets/*`) to the same relative paths under the target skill directory and records them in the manifest. Symlinks are rejected.
- Description-as-trigger is universal. The skill's body (everything after the closing `---`) is the instruction content the agent loads on trigger.
- Some skills mention `compatibility` as an optional frontmatter field in their body text, but none of the corpus examples actually used it in frontmatter. Treated as authorial intent, not schema.

## Deliberately Excluded Concepts

### Concept: `discovery_keywords`

Author convention for skill discoverability (tag-like). No surveyed
host (Claude Code, Gemini CLI, Codex CLI) parses `keywords` as a
first-class discovery field — skills are triggered by `description`
across all three. Below the 2-example floor, and adopting it would
not change install behaviour anywhere. Adapter behaviour: preserve
verbatim as pass-through metadata if the source carries it; never
flag as lossy (lossy_when_dropped: false). If a future host adopts
native keyword-based discovery, add it to `aliases` and flip
lossy_when_dropped to true.

**Field Names:** `keywords`

### Concept: `metadata_bucket`

Bucket field for non-standard sub-fields (single-author observed
`short-description` inside). No host parses it natively. Adapter
behaviour: pass-through if present; never lossy. If multiple kinds
eventually need a metadata bucket, promote to a universal optional
field rather than per-kind extensions.

**Field Names:** `metadata`

### Concept: `skill_version_metadata`

Version metadata carried by Hermes-first skills. Treated as
pass-through metadata rather than a universal core field because
no surveyed non-Hermes host parses it semantically. Adapter
behaviour: preserve verbatim when present; never flag as lossy.

**Field Names:** `version`

### Concept: `skill_author_metadata`

Author metadata carried by Hermes-first skills. Same policy as
`version`: preserve verbatim as pass-through metadata; do not
treat dropping it as semantically lossy.

**Field Names:** `author`

### Concept: `hermes_skill_platform_filter`

Hermes-native platform gating for skills (`linux`, `macos`,
`windows`). Load-bearing: Hermes uses it to decide whether a skill
is available on the current OS. Adapter behaviour:
- hermes adapter: emit natively when present.
- non-hermes adapters: surface as lossy. Silently dropping a
  platform filter can make a skill available on hosts/OSes the
  author explicitly excluded.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `hermes` | `platforms` |

### Concept: `hermes_skill_required_environment`

Hermes-native preflight requirements for skills. These fields
declare environment variables or credential files the skill needs
before execution. Adapter behaviour:
- hermes adapter: emit natively when present.
- non-hermes adapters: surface as lossy. Dropping them removes a
  real runtime precondition check and makes failure mode much less
  clear for the user.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `hermes` | `required_environment_variables` |
| `hermes` | `required_credential_files` |

### Concept: `claude_skill_runtime_overrides`

Documented in the Claude Code SKILL.md spec but absent from every
corpus example. These are per-tool runtime concerns — tool
permissions (`allowed-tools`), model selection (`model`), subagent
dispatch (`context`, `agent`), CLI ergonomics (`argument-hint`),
invocation control (`disable-model-invocation`, `user-invocable`).
Grouped into one canonical_concept for now because corpus presence
is zero across all of them; splitting into per-concept entries
(claude_skill_tool_permissions, claude_skill_model_override, ...)
is a follow-up if future corpus surfaces any of them above floor.
Adapter behaviour:
- claude-code adapter: emit natively when present.
- non-claude-code adapters: surface as lossy (the field has runtime
  meaning on Claude that other hosts cannot honour). Install
  proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `claude-code` | `allowed-tools` |
| `claude-code` | `model` |
| `claude-code` | `context` |
| `claude-code` | `agent` |
| `claude-code` | `argument-hint` |
| `claude-code` | `disable-model-invocation` |
| `claude-code` | `user-invocable` |

