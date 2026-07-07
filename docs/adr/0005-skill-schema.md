# Skill schema derived empirically from 10-source corpus

## Context

Per [ADR-0001](./0001-empirically-derived-schema-via-corpus-survey.md), each kind's schema is derived from a curated real-world corpus rather than designed top-down. `skill` is the first kind surveyed. The corpus and survey methodology (`schema-corpus.yaml` + `scripts/survey.sh`) are intended to be reproducible — a future re-run after ecosystem drift should produce comparable results.

## Decision

`schema/skill.yaml` defines three frontmatter fields: `name` (required, 10/10), `description` (required, 10/10), `license` (optional, 3/10). Plus the on-disk template: a directory containing `SKILL.md` with YAML frontmatter and a required markdown body.

Deliberately excluded:
- `keywords` (1/10), `metadata` (1/10) — below the 2-example floor.
- `allowed-tools`, `model`, `context`, `agent`, `argument-hint`, `disable-model-invocation`, `user-invocable` (0/10) — documented in the Claude Code SKILL.md spec but absent from every corpus example.

## Why

The corpus was 10 sources across four authors (anthropics/skills × 4, openai/skills × 1, obra/superpowers × 3, daymade/claude-code-skills × 2). The diversity goal was variation in author conventions, length, and optional fields — not coverage of every documented field. Survey ran via `gemini -p` (Gemini CLI 0.40) through `scripts/runtime/gemini.sh`, which binds the workdir-handoff contract from [archived ADR-0004](../archive/adr/0004-workdir-filesystem-handoff-agent-interface.md).

**Universal-vs-adapter boundary.** Per [ADR-0003](./0003-universal-kinds-with-adapter-capability-matrix.md), the universal schema captures content metadata that crosses agent hosts; per-tool runtime fields belong in the adapter capability matrix. `name`, `description`, and the markdown body are universal — every host treats them the same way. `license` is corpus-supported and pass-through-only. The Claude-spec extended fields (`allowed-tools`, `model`, `context: fork`, etc.) are runtime concerns: which tools auto-approve, which model to invoke, whether to fork a subagent. None of these survive a port to Codex or Gemini without re-mapping, so they belong in adapter logic — translator strips or remaps; adapters that natively support them re-emit on install.

**License is corpus-supported but not universal.** It appears 3/4 in anthropics/skills and 0/6 elsewhere. We kept it as optional and pass-through because (a) it meets the 2-example floor, (b) license metadata is a real cross-ecosystem need even if conventions diverge, and (c) the cost of supporting an optional string field is negligible. The ADR documents the concentration so future readers don't mistake "license: optional" for "license: universally adopted."

**Single-occurrence fields excluded.** `keywords` (qa-expert only) and `metadata` (gh-address-comments only) appeared once each. The survey prompt's 2-example floor rule was designed to keep one-author conventions out of the universal schema; we apply it here. Translators may preserve these as pass-through; adapters MAY surface them in native UIs; the schema does not require them.

## Consequences

**Survey methodology refinements** surfaced by this first run:
1. The survey agent miscounted `license` as 4/10 (actual: 3/10). Hand-checked with grep. Future runs should require the agent to grep-verify counts in `clusters.md`, not just summarise.
2. The agent included two 1/10 fields despite the prompt's 2-example floor, justifying with vibes ("aligns with package-manager patterns"). Survey prompt needs tightening before the next kind: "If you include any field with fewer than 2 occurrences, explain in clusters.md exactly why installation would be impossible without it."

These refinements should be applied to `scripts/survey.sh` before running the survey for `agent`. Tracked as part of task #3 follow-up, not a blocker for committing `schema/skill.yaml`.

**Downstream implications:**
- The validator (forthcoming) gates installs on `name` + `description` + the markdown body existing. Everything else is optional.
- The translator's job for `skill` is the smallest of any kind: most "wild" skills already conform. Translation effort concentrates on (a) extracting frontmatter from skills that put it in odd places (README-style headers, alternate filenames), and (b) mapping the spec-extended fields onto adapter capability declarations.
- Adapter capability matrices for `skill` should be `native` across `claude-code` and `agents-cli` (Gemini + Codex via `~/.agents/skills/`). No lossy mapping expected.
- Re-running the survey when the ecosystem evolves (e.g., if `allowed-tools` starts appearing in real-world frontmatter at meaningful frequency) is cheap; the corpus file gives a reproducible baseline.

## Artefacts

- `schema/skill.yaml` — the committed schema.
- `schema-corpus.yaml` (kind: skill) — the 10 sources.
- `.dotpack-workdirs/survey/skill/` — preserved survey workdir with `prompt.md`, `input/1-10--SKILL.md`, `output/schema.yaml`, `output/clusters.md`. Not version-controlled (under `.dotpack-workdirs/`).
- `scripts/runtime/gemini.sh` — the runtime binding that satisfies the ADR-0004 workdir contract.
