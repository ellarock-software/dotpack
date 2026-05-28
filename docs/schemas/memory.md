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

- Filename divergence (CLAUDE.md vs GEMINI.md vs AGENTS.md) is the primary cross-tool concern. AGENTS.md is the converging standard (multiple hosts honour it), but Claude and Gemini still prefer their branded variants when present.
- Gemini CLI supports `@file.md` import syntax inside GEMINI.md for modularisation. Claude Code and Codex do not honour this syntax. The translator should resolve imports when porting Gemini-style modular memories to a flat host.
- Scope is hierarchical: a subdirectory memory file applies only to work within that subtree. dotpack preserves the filesystem location of the source; adapters install to a matching path in the target.

