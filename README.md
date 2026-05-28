# dotpack

dotpack is a local package manager and translator for AI-agent resources.
It validates a portable resource shape, then installs that resource into the
native files used by Claude Code, Gemini CLI, Antigravity CLI, Codex CLI, or the `agents-cli`
umbrella target.

The project is intentionally filesystem-first. Adapters produce install plans;
the orchestrator applies those plans, records provenance in a dotpack manifest,
and uses that manifest for list, uninstall, reconcile, and prune operations.

## Universal Translation Architecture

`dotpack` serves as a universal translation middleware for AI-agent configurations. It bridges the gap between different agent ecosystems through a two-step transformation process:

1. **Ingestion (`dotpack import`)**: Transforms native host configurations (e.g., `claude-code` files, including custom hooks, skills, and agents) into a common, standard `.agents` schema.
2. **Ejection / Fan-out (`dotpack install`)**: Translates and installs from the standard `.agents` schema into specific native host formats (`claude-code`, `gemini-cli`, `codex`, `antigravity-cli`, etc.). The `agents-cli` umbrella target automatically translates and fans out the configuration to all supported host environments simultaneously.

**Important Behavioral Distinction**: `dotpack` guarantees the *translation and placement* of these configurations. If an external post-install lifecycle script (such as Sponsio) fails because a target host is unregistered in that external tool, this is an external enforcement failure—not a `dotpack` translation failure. `dotpack` successfully completes the configuration transform.

## What dotpack can do

- Install portable resources into host-native layouts with
  `dotpack install`.
- Translate `.agents` resources to `.claude`, `.gemini`, `.codex/config.toml`,
  and shared `.agents/skills` targets depending on resource kind and host.
- Use `--agent agents-cli` to target Gemini CLI and Codex together. Skills are
  written once to the shared `.agents/skills` convergence path; hooks and MCP
  servers fan out to each host config file.
- Run declarative post-install lifecycle tasks after materialization. The
  bundled Sponsio task targets Codex, Gemini CLI, Antigravity CLI, and the
  `agents-cli` umbrella; it installs Sponsio when missing, runs
  `sponsio host install all --mode enforce`, and fails closed if verification
  cannot prove all three host integrations.
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
dotpack install .agents/skills/code-review/SKILL.md --agent antigravity-cli --scope project
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

| Kind | `claude-code` target | `gemini-cli` target | `antigravity-cli` target | `codex` target | `agents-cli` target |
| --- | --- | --- | --- | --- | --- |
| `skill` | `.claude/skills/<name>/SKILL.md` | `.gemini/skills/<name>/SKILL.md` | `.antigravity/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` once for sub-adapters |
| `agent` | `.claude/agents/<name>.md` | `.gemini/agents/<name>.md` | `.antigravity/agents/<name>.md` | unsupported | unsupported |
| `mcp-server` | `.mcp.json` or `~/.claude.json` | `.gemini/settings.json` | `.antigravity/settings.json` | `.codex/config.toml` | fans out to sub-adapter config files |
| `hook` | `.claude/settings.json` | `.gemini/settings.json` | `.antigravity/settings.json` | `.codex/config.toml` | fans out to sub-adapter config files |

For user scope, the same host roots resolve under `~/.claude`, `~/.gemini`,
`~/.antigravity`, `~/.agents`, `~/.codex`, and `~/.dotpack`. For project scope, dotpack anchors
paths at `DOTPACK_PROJECT_HOME` or the current working directory.

Important boundaries:

- `command` and `memory` schemas exist, but the CLI currently rejects those
  kinds until adapters land.
- Post-install lifecycle tasks live in `internal/cli/lifecycle_tasks.yaml`, not
  in host adapters. The bundled Sponsio task is mandatory for `codex`,
  `gemini-cli`, `antigravity-cli`, and `agents-cli` installs; if Sponsio or a
  configured installer is unavailable, or Sponsio cannot verify any required
  host, dotpack reports the materialized resource and exits with an error.
  This is a runtime support gate, not a dotpack config fiction: OpenAI Codex
  CLI 0.125.0 serializes hook stdin with `hook_event_name`, `tool_name`, and
  `tool_input`, and accepts the Claude-style `hookSpecificOutput` deny reply.
  A Sponsio binary still has to register the `codex`, `gemini-cli`, and
  `antigravity-cli` hosts; `sponsio host install all` only covers registered
  hosts, so dotpack verifies each required host explicitly.
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
