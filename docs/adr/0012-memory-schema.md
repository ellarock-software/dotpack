# Memory schema: shape-divergent, conventions-first

## Context

Per [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md). Fourth kind surveyed. The `memory` kind is structurally different from `skill`/`agent`/`command` — these are full markdown files with no frontmatter and no field-validation contract. The hosts read the entire file as context. The schema describes filename conventions, scope, length norms, and recurring section conventions — not a frontmatter contract.

## Decision

`schema/memory.yaml` captures shape and conventions, not fields:

- **No frontmatter.** 0/7 corpus examples have YAML frontmatter. The schema documents this as the canonical shape; sources with frontmatter get it stripped (or comment-prepended into the body) during translation.
- **Three filename variants:** `CLAUDE.md`, `GEMINI.md`, `AGENTS.md`. dotpack treats them as interchangeable at the kind level; adapters write to the host-native filename.
- **Scope is hierarchical:** root-of-repo (common) and subdirectory (monorepo subpackage scoping). dotpack preserves source location during install.
- **Recurring sections** (overview, conventions, testing, workflow, commands, documentation, structure) are catalogued as author conventions and aliases — NOT required fields. Survey identified them at frequencies 2/7 to 6/7.
- **Length norms** observed: 19–221 lines, median 96. Bimodal distribution. dotpack does not enforce length.

## Why

**Treating sections as fields would mis-shape the schema.** The survey produced an output with 8 "fields" (overview, testing, conventions, etc.) all marked `required: false`. Empirically reasonable, but conceptually wrong — none of these are *fields* in any meaningful sense. They're section names in a free-form markdown document. A validator can't usefully gate on them, an adapter can't usefully translate them, and forcing the structure on translated output would damage the source. Kept the survey's clusters as documentation under `recurring_sections` so downstream tools (e.g., a memory-extractor) can use them as hints — not as schema gates.

**AGENTS.md as the cross-tool standard.** The corpus has 2 AGENTS.md examples (openai/codex, openai/openai-cookbook), 3 CLAUDE.md examples, and 2 GEMINI.md examples. The agents.md initiative (cross-tool spec) is the convergence path; Codex reads only AGENTS.md, Claude Code reads CLAUDE.md but honours AGENTS.md as a fallback, Gemini CLI prefers GEMINI.md. dotpack's adapter capability matrix should treat all three as a single logical install target — the universal kind is `memory`, the filename is adapter-determined.

**Hierarchical scope is real and worth preserving.** `google-gemini/gemini-cli` ships GEMINI.md at root AND `packages/cli/GEMINI.md` for the CLI subpackage. The subpackage memory applies only when working in that subtree. dotpack preserves filesystem location when installing — a memory resource with `target_dir: packages/cli/` installs there, not at root.

**Gemini-specific `@file.md` import syntax noted but not adopted.** Gemini CLI supports `@file.md` inline imports inside memory files for modularity. Claude and Codex don't recognize this. dotpack's translator should resolve these imports when porting a Gemini-style modular memory to a flat-file host. The schema doesn't bless or forbid imports; it's a translator concern.

**Methodology refinement worth noting.** The survey treated `memory` as if it had fields like the other kinds, producing field-shaped output. The pre-existing ADR-0009 note about memory being shape-divergent saved us — we hand-reshaped the schema rather than committing the field-shaped one. A future survey iteration could split the prompt: for `full_file` shape, produce a `sections`/`length_norms` output, not a `fields` output. Tracked, not blocking.

## Consequences

**Validator simplicity.** The memory validator's only check is "is this a valid markdown file?". No required field, no required section. This is intentional — over-validation here would break legitimate authoring patterns (a memory file that's all H1s, or one with no headers at all, are both legitimate).

**Translator complexity is in import resolution and filename selection.** The Gemini `@file.md` import resolution is the main translation work. Filename mapping is mechanical (host → filename).

**Adapter capability matrix.**
- `claude-code`: native for CLAUDE.md and AGENTS.md (reads both with CLAUDE.md preferred). Writes to `<scope>/CLAUDE.md`.
- `agents-cli` (Gemini/Codex):
  - Gemini side: native GEMINI.md. Writes to `<scope>/GEMINI.md`.
  - Codex side: native AGENTS.md. Writes to `<scope>/AGENTS.md`.
  - This is another fan-out case for the agents-cli adapter (task #4).

**Install-target scope is a new concept** the schema introduces explicitly. Prior kinds (skill/agent/command) install into a fixed directory per host. Memory installs into a *user-specified* subdirectory. Manifest format (per ADR-0008) already has `target_dir` field — fits naturally.

## Artefacts

- `schema/memory.yaml`
- `schema-corpus.yaml` (kind: memory) — 7 examples across 3 filename variants
- `.dotpack-workdirs/survey/memory/`
