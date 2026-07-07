# Memory Schema

**Version:** `0`

## Template

- **Shape:** `full_file`
- **Filename:** ``

## Fields

| Name | Type | Required | Notes |
| --- | --- | --- | --- |
| `frontmatter` | `none` | No | Memory files in the corpus have NO YAML frontmatter. The entire file is the prompt-context content. dotpack does not add frontmatter during translation; sources that contain it would have the frontmatter stripped or pre-pended into the body as a comment. |

## Ecosystem Notes

- Filename divergence (CLAUDE.md vs GEMINI.md vs AGENTS.md) is the primary cross-tool concern. AGENTS.md is the converging standard (multiple hosts honour it), but Claude and Gemini still prefer their branded variants when present. Hermes adds two more project-local filenames (`.hermes.md`, `HERMES.md`) plus a user-scoped global identity file (`SOUL.md`).
- Gemini CLI supports `@file.md` import syntax inside GEMINI.md for modularisation. Claude Code and Codex do not honour this syntax. The translator should resolve imports when porting Gemini-style modular memories to a flat host.
- Hermes context-file priority is `.hermes.md` / `HERMES.md` → `AGENTS.md` → `CLAUDE.md` → `.cursorrules`, with `SOUL.md` loaded independently from `HERMES_HOME` as the agent identity.
- Scope is hierarchical: a subdirectory memory file applies only to work within that subtree. dotpack preserves the filesystem location of the source; adapters install to a matching path in the target.

