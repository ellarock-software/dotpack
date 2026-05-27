# dotpack

dotpack is a local package manager and translator for AI-agent resources.
It validates a portable resource shape, then installs that resource into the
native files used by Claude Code, Gemini CLI, Codex CLI, or the `agents-cli`
umbrella target.

The project is intentionally filesystem-first. Adapters produce install plans;
the orchestrator applies those plans, records provenance in a dotpack manifest,
and uses that manifest for list, uninstall, reconcile, and prune operations.

## What dotpack can do

- Install portable resources into host-native layouts with
  `dotpack install`.
- Translate `.agents` resources to `.claude`, `.gemini`, `.codex/config.toml`,
  and shared `.agents/skills` targets depending on resource kind and host.
- Use `--agent agents-cli` to target Gemini CLI and Codex together. Skills are
  written once to the shared `.agents/skills` convergence path; hooks and MCP
  servers fan out to each host config file.
- Reject lossy installs by default when a source field has host-specific
  runtime meaning that the target cannot honor. Pass `--allow-lossy` only when
  that loss is intentional.
- Import durable Claude Code project configuration into a canonical `.agents`
  tree with `dotpack import claude-code ... --out ...`.
- Track ownership in `~/.dotpack/installs.yaml`, then remove owned files or
  merged config keys with `dotpack uninstall`.
- Report and clean manifest drift with `dotpack reconcile` and `dotpack prune`.

## Quick Start

Run the CLI from source:

```sh
go run ./cmd/dotpack --help
```

Install a portable skill from a checked-in `.agents` tree:

```sh
dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope project
dotpack install .agents/skills/code-review/SKILL.md --agent gemini-cli --scope project
dotpack install .agents/skills/code-review/SKILL.md --agent codex --scope project
```

Install config fragments:

```sh
dotpack install .agents/mcp-servers/github.mcp.json --kind mcp-server --agent codex --scope user
dotpack install .agents/hooks/bash-guard.hook.json --kind hook --agent agents-cli --scope project
```

Import a Claude Code tree into `.agents`:

```sh
dotpack import claude-code /path/to/project --out /path/to/project
```

## Translation Targets

`dotpack install` is the `.agents` to host-native translation path. It installs
one resource at a time.

| Kind | `claude-code` target | `gemini-cli` target | `codex` target | `agents-cli` target |
| --- | --- | --- | --- | --- |
| `skill` | `.claude/skills/<name>/SKILL.md` | `.gemini/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` once for Gemini + Codex |
| `agent` | `.claude/agents/<name>.md` | `.gemini/agents/<name>.md` | unsupported | unsupported |
| `mcp-server` | `.mcp.json` or `~/.claude.json` | `.gemini/settings.json` | `.codex/config.toml` | fans out to Gemini + Codex config files |
| `hook` | `.claude/settings.json` | `.gemini/settings.json` | `.codex/config.toml` | fans out to Gemini + Codex config files |

For user scope, the same host roots resolve under `~/.claude`, `~/.gemini`,
`~/.agents`, `~/.codex`, and `~/.dotpack`. For project scope, dotpack anchors
paths at `DOTPACK_PROJECT_HOME` or the current working directory.

Important boundaries:

- `command` and `memory` schemas exist, but the CLI currently rejects those
  kinds until adapters land.
- `import` is native to `.agents`. Today it supports Claude Code input only.
- There is no bulk exporter yet; install the specific `.agents` resource you
  want to materialize for a host.
- Codex skills live in `.agents/skills`, not `.codex/skills`. Codex MCP servers
  and hooks live in `.codex/config.toml`.

## Resource Shapes

- `skill`: a directory with `SKILL.md` frontmatter and Markdown body.
- `agent`: a Markdown file named `<agent-name>.md` with frontmatter and body.
- `mcp-server`: a JSON fragment shaped as `{"mcpServers": {"<name>": {...}}}`.
- `hook`: a JSON fragment shaped around a top-level `hooks` map.

The schemas live in `schema/*.yaml`. The Go parsers live under
`internal/resource`, validators under `internal/validator`, host adapters under
`internal/adapter`, and CLI command wiring under `internal/cli`.

## Development

Run the full local test suite:

```sh
go test ./...
```

High-value entry points for AI agents:

- `internal/cli/install.go`: supported `--agent` values, kind inference,
  umbrella fan-out, and install help.
- `internal/orchestrator/orchestrator.go`: install apply path, manifest
  records, collision handling, and lossy-field errors.
- `internal/orchestrator/umbrella.go`: `agents-cli` fan-out behavior.
- `internal/adapter/filedrop`: skill and agent file writes.
- `internal/adapter/configfrag`: hook and MCP server config merges.
- `internal/dirs/dirs.go`: environment variables and path resolution.

When changing behavior, update the relevant CLI help and README text together.
