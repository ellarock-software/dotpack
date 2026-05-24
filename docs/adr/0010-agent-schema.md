# Agent schema derived empirically from 8-source Claude-biased corpus

## Context

Per [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md). Second kind surveyed (after `skill`, [ADR-0009](./0009-skill-schema.md)). Agents are markdown-with-frontmatter files that become a subagent's system prompt when invoked. Both Claude Code and Gemini CLI document agent-kind support; real-world adoption is heavily Claude-skewed.

## Decision

`schema/agent.yaml` defines four frontmatter fields: `name` (required, 8/8), `description` (required, 8/8), `model` (optional, 8/8), `tools` (optional, 5/8). Body is required markdown.

Deliberately excluded:
- Claude-spec fields absent from corpus (`maxTurns`, `disallowedTools`, `permissionMode`, `skills`, `mcpServers`, `hooks`, `memory`) — adapter-layer.
- Gemini-spec fields with no Claude corpus presence (`kind`, `temperature`, `max_turns`, `timeout_mins`) — adapter-layer.
- Body-section normalisation (survey proposed canonical "## Approach" / "## Workflow" sections) — author convention, not schema.

## Why

**Corpus composition.** 8 agents across 4 curators (wshobson × 1, VoltAgent × 3, 0xfurai × 2, lst97 × 2). All Claude-flavoured because real-world `.gemini/agents/*.md` adoption is sparse (the spec exists and ships with the Gemini CLI docs, but published-and-popular third-party Gemini agents are not yet findable in the way Claude subagent collections are). The bias is acknowledged here so it can be re-balanced when the Gemini agent ecosystem matures.

**Why `model` is optional despite 8/8 presence.** Marking 100%-present fields as required is the empirically-honest default. We violate that default here because the published Gemini CLI spec explicitly makes `model` optional (defaults to inheriting the parent session's model). The 8/8 corpus presence reflects a Claude-author convention to set the model explicitly; making `model` required would force every Gemini-style agent through the translator just to add a field the host wouldn't have demanded. Schema is for the ecosystem, not just the corpus.

**Why `tools` is optional with mixed type.** 5/8 corpus presence. The survey marked it required at 5/8 with an "installation failure mode" rationale ("non-tool agents are chatter-only") — wrong analysis: agents with no `tools` declaration inherit the host's default tool grant, which is functional, not chatter-only. The corpus proves this: 3/8 agents omit `tools` entirely and are still functional subagents. Type is `string | array<string>` because Claude convention is a comma-separated string and Gemini convention is a YAML array; the translator normalises internally, adapters re-emit native shape.

**Universal-vs-adapter boundary** (same as ADR-0009). The universal schema captures content metadata (who am I, what do I do, what model do I want, what tools do I expect). Runtime configuration (permission modes, max turns, temperature, inline MCP server declarations) varies per host and lives in the adapter capability matrix.

**The `kind` naming clash.** Gemini's spec includes a frontmatter field literally named `kind` with values `local` or `remote`. dotpack's own taxonomy also uses "kind" (skill/agent/command/...). Inside a `.gemini/agents/*.md` file the host-level `kind: local` field would survive translation and confuse a reader who knows dotpack's vocabulary. The translator should preserve it during round-trip but the dotpack schema does not adopt the term — see `template.shape` instead.

## Consequences

**Methodology refinement noted but not fixed yet.** Even with the tightened prompt from ADR-0009, the agent marked `tools` as `required: true` despite 5/8 presence. The new failure-mode rule was honoured (rationale was concrete: "non-tool agents are chatter-only"), but the rationale was *wrong*. The schema decision was hand-corrected. Next prompt iteration should add: "Fields present in fewer than 100% of examples are optional by default. Marking sub-100% fields as required requires evidence in the corpus that omission breaks installation, not a hypothetical scenario." Tracked but not blocking — applies to remaining kinds (command, memory, hook, mcp-server).

**Body-section recommendations preserved separately.** The survey produced a thoughtful clustering of body sections (`## Approach`, `## Workflow`, `## Quality Checklist`, etc.). dotpack does not enforce these, but the survey's `clusters.md` is preserved in the workdir as author-facing recommendations. If dotpack ever adds an `agent-creator` scaffolder it can consume this.

**Adapter implications.**
- `claude-code` adapter: `tools` comma-string format is native. Agent goes into `.claude/agents/<name>.md` (project) or `~/.claude/agents/<name>.md` (user).
- `agents-cli` adapter (Gemini + Codex via `~/.agents/`): the convergent `~/.agents/agents/` path is **not yet honoured** by either Gemini or Claude for the agent kind (skills converge on `~/.agents/skills/`; agents do not). agents-cli must fan out to each host's native agent directory. This is the same shape of problem as task #4 (hook/mcp-server fan-out) — `agent` joins the list.

## Artefacts

- `schema/agent.yaml`
- `schema-corpus.yaml` (kind: agent)
- `.dotpack-workdirs/survey/agent/` — preserved survey workdir
