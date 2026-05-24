# Command schema spanning two on-disk formats (markdown+YAML, TOML)

## Context

Per [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md). Third kind surveyed. The `command` kind has a known cross-ecosystem format split flagged in the original handoff: Claude Code stores commands as markdown with optional YAML frontmatter under `.claude/commands/*.md`; Gemini CLI stores commands as TOML under `.gemini/commands/*.toml`. The corpus deliberately includes both.

## Decision

`schema/command.yaml` defines one universal field set, agnostic of file format:
- `prompt` (required, 7/7) — the instruction text. Lives in the markdown body for `.md` commands; lives in the `prompt =` TOML field for `.toml` commands.
- `description` (optional, 4/7) — one-line summary.
- `model` (optional, 3/7) — Claude-side per-command model selection.
- `allowed-tools` (optional, 1/7) — kept despite below-floor; see "Why" below.

The template declares two parallel `formats` — markdown with YAML frontmatter, and TOML — so the validator + adapters know both shapes are canonical.

## Why

**Unifying body and `prompt` field.** Markdown commands have no `prompt:` field — the markdown body IS the prompt. TOML commands have an explicit `prompt = "..."` field and no body. The survey correctly identified these as the same conceptual slot and unified on the name `prompt` (7/7). Going with `prompt` over `body` because (a) it names the semantic concept (instructions for the LLM) rather than a structural artefact, (b) Gemini's documented spec already uses it, (c) the translator becomes trivial: round-tripping is "where does prompt live in this format?" rather than name remapping.

**`allowed-tools` kept despite 1/7 presence.** This is the second time we've explicitly violated the floor rule in an ADR (after `model` for agent — 8/8 corpus but optional per spec). The reasoning here cuts the other way: `allowed-tools` is *below* the floor in corpus presence but materially changes runtime semantics in Claude Code. Without `allowed-tools`, every Bash invocation in a command body prompts the user for approval; with it, listed tools are auto-approved. Stripping `allowed-tools` during translation would silently degrade UX. The field is also documented as Claude's official command frontmatter reference field. Adapter capability matrices handle non-supporting hosts.

**Argument placeholder syntax not normalised.** Claude uses `$ARGUMENTS` (and positional `$1`, `$2`); Gemini uses `{{arg0}}` double-curly templating. The translator handles this when porting a command across formats. The schema does not force one — both are legal in their native formats.

**Subdirectory→namespace convention is universal.** Both Claude and Gemini derive command names from file paths, with subdirectories creating namespaces. Claude uses subdirectory prefixes (`git/commit.md` → invoked as something like `/git:commit` depending on the host); Gemini uses path-to-colon translation (`agents/start.toml` → `/agents:start`). This is a template-shape consequence and is documented in `schema/command.yaml` rather than as a separate field.

**Methodology continues to improve.** The tightened prompt (post-ADR-0010) finally honoured the floor rule unprompted — the agent excluded `allowed-tools` empirically and we re-added it manually with concrete reasoning. Grep-verified counts matched the agent's reported counts (description 4/7, model 3/7, allowed-tools 1/7). No corrections needed beyond the deliberate `allowed-tools` add-back.

## Consequences

**Translator complexity.** The `command` translator is the most format-aware of any kind so far. Ports must handle:
- Format conversion (markdown body ↔ TOML `prompt = """..."""` field)
- Frontmatter conversion (YAML → TOML for non-prompt fields, or strip Claude-only fields like `model` when targeting Gemini)
- Argument-placeholder rewrite ($ARGUMENTS ↔ {{arg0}})
- Filename and subdirectory layout (which the host derives the command name from)

**Adapter capability matrix entries.**
- `claude-code`: native for markdown+YAML; produces a `.md` file with frontmatter. Native support for `allowed-tools`, `model`, `description`. Does NOT support Gemini's TOML form (would need format conversion at install).
- `agents-cli` (Gemini/Codex side): native for TOML; produces `.toml` files. No `allowed-tools` analog → adapters mark this lossy if the source command carries it. Codex CLI's command format is unclear from this corpus — assume Gemini's TOML for now, revisit if Codex diverges.

**Open: how does the install pipeline pick the target format?** A user installing a `command` kind into `agents-cli` would presumably want TOML (the format the host honors); into `claude-code`, markdown. The adapter declares which format it emits — universal schema doesn't decide. This needs concretising in adapter-layer code, not schema.

## Artefacts

- `schema/command.yaml`
- `schema-corpus.yaml` (kind: command) — 4 Claude .md + 3 Gemini .toml
- `.dotpack-workdirs/survey/command/`
