# Rule schema: named Markdown guidance with preserved introduction metadata

## Context

`reia-config` introduced shared host-agnostic guidance under `.agents/rules/`,
starting with `.agents/rules/graphify.md`. The file is not a skill, agent,
hook, MCP fragment, or memory file: it is a named instruction rule that should
materialize into each host's native rule/instruction surface.

The source also carries artifact introduction metadata (`artifact-type`,
`owner/surface`, `purpose`, `triggers`, `inputs`, `outputs`, `state-read`,
`state-written`, `registered-in`, `tests`, `failure-mode`,
`host-compatibility`). Treating those fields as unknown extensions made dotpack
require `--allow-lossy` even though the metadata is authoring/provenance data
that should be understood and preserved.

## Decision

Add universal kind `rule`.

- Canonical source: `.agents/rules/<name>.md`.
- Shape: Markdown file with YAML frontmatter and body.
- Identity: `name` preferred, `id` fallback.
- Project outputs:
  - Claude Code: `.claude/rules/<name>.md`
  - Gemini CLI: `.gemini/rules/<name>.md`
  - Codex: `.codex/rules/<name>.md`
  - agents-cli umbrella: one manifest record, fan-out to Gemini CLI and Codex.
- User outputs mirror the same `rules/<name>.md` path under the host home.
- Introduction metadata is schema-known pass-through data with
  `lossy_when_dropped: false`; adapters preserve it byte-for-byte when the
  source is already a Markdown rule file.

When a direct shared source `.agents/rules/<name>.md` is installed, dotpack also
removes legacy host-specific canonical copies such as
`.agents/rules/gemini/<name>.md` and `.agents/rules/claude/<name>.md`. Host
outputs like `.gemini/rules/<name>.md` are not removed; they are updated by the
target adapter and tracked in the manifest.

## Consequences

`dotpack list` and `dotpack reconcile` need no rule-specific code because rule
installs are file claims in the normal manifest model.

The rule schema deliberately keeps introduction metadata out of the universal
runtime core. If a future host gives one of those fields operational semantics,
split it into its own schema concept with host aliases and default lossy
behavior.
