# Rule Schema

**Version:** `0`

## Template

- **Shape:** `file_with_frontmatter`
- **Filename:** ``
- **Body:** `required`

## Fields

| Name | Type | Required | Notes |
| --- | --- | --- | --- |
| `id` | `string` | `one_of_id_or_name` | Stable canonical identifier. Used for install identity when `name` is absent. |
| `name` | `string` | `one_of_id_or_name` | Stable canonical identifier. Preferred for install identity when present. |
| `body` | `markdown` | **Yes** | Rule guidance loaded by the host. |

## Ecosystem Notes

- Rules are modular host guidance. They are narrower than memory files such as AGENTS.md/CLAUDE.md/GEMINI.md and are installed as named files under each host's native rules directory.
- Introduction metadata is authoring/provenance data. Adapters preserve it in emitted Markdown rule files and do not require --allow-lossy for those fields.

## Deliberately Excluded Concepts

### Concept: `artifact_introduction_metadata`

**Field Names:** `artifact-type`, `owner`, `owner/surface`, `purpose`, `triggers`, `inputs`, `outputs`, `state-read`, `state-write`, `state-written`, `registered-in`, `tests`, `failure-mode`, `host-compatibility`, `title`, `description`, `keywords`, `patterns`, `agent`, `context`

