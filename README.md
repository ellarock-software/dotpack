# dotpack

dotpack is a filesystem-first package manager and translator for AI-agent
resources. It validates portable `.agents` resources, materializes them into
host-native files, and records provenance so installed output can be listed,
uninstalled, reconciled, reset, and reinstalled later.

## Why dotpack exists

Agent CLIs use similar concepts but different file layouts and config formats:
skills, agents, rules, commands, memory files, MCP servers, and hooks are not
portable by default. dotpack gives those resources a canonical `.agents` source
shape and translates that source into host-native output for supported tools.

```text
.agents resource -> dotpack install/install-all -> host-native files
host-native files -> dotpack import/sync-back -> canonical .agents
```

## Install

From source:

```sh
git clone https://github.com/ellarock-software/dotpack.git
cd dotpack
go build ./cmd/dotpack
```

With Go:

```sh
go install github.com/ellarock-software/dotpack/cmd/dotpack@latest
```

## Quick Start

Run the CLI from source:

```sh
go run ./cmd/dotpack --help
```

Install one resource:

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

Install every supported resource from a canonical tree into a target project:

```sh
dotpack install-all --from /path/to/project/.agents --target /path/to/project --agent agents-cli --scope project
```

Install from a repository with a non-canonical source layout:

```sh
dotpack install-all --from /path/to/catalog --skills-path skills --agent agents-cli --scope user
dotpack install-all --from /path/to/catalog --kind-path skill=skills --kind-path agent=agents --agent claude-code
dotpack install-all --from github:BuilderIO/skills --skills-path skills --agent agents-cli --scope user
```

Import a Claude Code tree into `.agents`:

```sh
dotpack import claude-code /path/to/project --out /path/to/project
```

## Commands

| Command | Purpose |
| --- | --- |
| `dotpack install <source-path>` | Install one portable resource into one host or umbrella target. |
| `dotpack install-all` | Discover and install supported direct resources from a canonical `.agents` tree or explicit source layout. |
| `dotpack inventory` | Classify materialized host file outputs against manifest claims and optional canonical output. |
| `dotpack sync-back` | Copy drifted or untracked materialized file-drop output back into canonical `.agents`. |
| `dotpack reset-materialized` | Remove materialized host output owned by dotpack for a target; optionally remove scanned untracked file-drop output. |
| `dotpack import` | Convert a native host tree into canonical `.agents`; currently supports Claude Code input. |
| `dotpack list` | List installed manifest records in stable manifest order. |
| `dotpack uninstall <name-or-id>` | Remove one installed resource by full ID or short name plus `--agent` and `--kind`. |
| `dotpack reconcile` | Read-only manifest drift report for missing files, changed file hashes, or missing merged keys. |
| `dotpack prune` | Remove manifest records whose recorded claims are all absent from disk. |
| `dotpack version` | Print the dotpack version. |

## Coverage: Product Intent vs Shipped Adapters

**Product intent.** dotpack aims for *universal coverage of LLM coding tools*
across every operation (`skill`, `agent`, `rule`, `command`, `memory`,
`mcp-server`, `hook`). The architecture is deliberately **open**: a host is a
self-contained adapter that self-registers, and a config format is a
self-contained merge backend that self-registers, so onboarding either does not
touch core switchboards. See [ADR-0014](docs/adr/0014-open-adapter-and-merge-backend-registries.md)
and [CONTRIBUTING.md](CONTRIBUTING.md).

**Currently shipped adapters.** The matrix below is the set implemented *today*,
not a closed boundary — "current adapters include…". Per-operation support is
genuine per-adapter data: a cell may read *unsupported* where a host has no
concept for that operation (e.g. OpenCode has no rule or hook surface), and
that is expressed by the adapter, not special-cased in core.

Project-scope paths are shown below. User-scope paths resolve under host home
directories such as `~/.claude`, `~/.gemini`, `~/.antigravity`, `~/.agents`,
`~/.codex`, and `~/.config/opencode`.

| Kind | `claude-code` | `gemini-cli` | `antigravity-cli` | `codex` | `opencode` | `agents-cli` |
| --- | --- | --- | --- | --- | --- | --- |
| `skill` | `.claude/skills/<name>/SKILL.md` | `.gemini/skills/<name>/SKILL.md` | `.antigravity/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` | `.opencode/skills/<name>/SKILL.md` | writes `.agents/skills/<name>/SKILL.md` once |
| `agent` | `.claude/agents/<name>.md` | `.gemini/agents/<name>.md` | `.antigravity/agents/<name>.md` | `.codex/agents/<name>.toml` | `.opencode/agents/<name>.md` | fans out to Gemini, Antigravity, and Codex |
| `rule` | `.claude/rules/<name>.md` | `.gemini/rules/<name>.md` | `.antigravity/rules/<name>.md` | `.codex/rules/<name>.md` | unsupported | fans out to Gemini, Antigravity, and Codex |
| `command` | `.claude/commands/<name>.md` | `.gemini/commands/<name>.toml` | `.antigravity/commands/<name>.md` | `.codex/commands/<name>.md` | `.opencode/commands/<name>.md` | fans out to each sub-adapter's command file |
| `memory` | `CLAUDE.md` | `GEMINI.md` | `ANTIGRAVITY.md` | `AGENTS.md` | `AGENTS.md` | fans out to each sub-adapter's memory file |
| `mcp-server` | `.mcp.json` or `~/.claude.json` | `.gemini/settings.json` | `.antigravity/settings.json` | `.codex/config.toml` | `opencode.json` (`$.mcp`) | fans out to Gemini, Antigravity, and Codex |
| `hook` | `.claude/settings.json` | `.gemini/settings.json` | `.antigravity/settings.json` | `.codex/config.toml` | unsupported | fans out to Gemini, Antigravity, and Codex |

`agents-cli` is an umbrella target, not a separate runtime. It fans out to the
compatible host adapters while preserving the user-typed `agents-cli` identity
in the dotpack manifest. As of [ADR-0014](docs/adr/0014-open-adapter-and-merge-backend-registries.md)
it covers all seven operations (`command` and `memory` now fan out to each
sub-adapter's own file).

**Config-merge formats.** Merged config writes (mcp-server, hook) go through a
pluggable backend keyed by file extension: `.json`, `.toml`, and `.yaml`/`.yml`
ship today. A new format is one `mergeBackend` implementation plus one
`registerBackend` call.

### Gap register (named, not hidden)

The architecture is open, but some work remains irreducibly host-specific or is
deferred backlog. Tracked explicitly so it is not mistaken for "done":

- **codex `.agents/skills` write-once convergence** stays a documented special
  case in the `agents-cli` umbrella (the shared path both codex and gemini read).
- **Per-host hook event-name remaps** (`PreToolUse`/`PostToolUse` ↔
  `BeforeTool`/`AfterTool`) live in each adapter's emit functions — host-specific
  by nature.
- **OpenCode extension fidelity**: OpenCode is not yet listed in `schema/*.yaml`
  host aliases, so resource *extensions* are dropped (treated lossy) on opencode
  installs; universal-core fields install fine. Adding opencode aliases to the
  schemas is the follow-up.
- **Pi and Hermes adapters** are named backlog — the onboarding path is
  mechanical (see CONTRIBUTING.md); only the per-host paths need confirming.

## Resource Shapes

The schemas live in `schema/*.yaml`; parsers live under `internal/resource`.

| Kind | Canonical shape |
| --- | --- |
| `skill` | A directory containing `SKILL.md` with frontmatter and Markdown body. `--kind` is inferred when the source file is named `SKILL.md`. |
| `agent` | A Markdown file with frontmatter and body, normally `.agents/agents/<name>.md`. Direct install requires `--kind agent`. |
| `rule` | A Markdown file at `.agents/rules/<name>.md` with YAML frontmatter containing `id` or `name` and a Markdown body. Direct `.agents/rules/*.md` installs infer `--kind rule`. |
| `command` | A Markdown command with YAML frontmatter and body, or a TOML command with a `prompt` field. Direct `.agents/commands/*.md|.toml` installs infer `--kind command`. |
| `memory` | A whole Markdown memory file such as `CLAUDE.md`, `GEMINI.md`, `AGENTS.md`, or `ANTIGRAVITY.md`; no frontmatter is required. |
| `mcp-server` | A JSON fragment shaped as `{"mcpServers": {"<name>": {...}}}`. Direct install requires `--kind mcp-server`. |
| `hook` | A JSON fragment shaped around a top-level `hooks` map. The source filename supplies the install name. `.agents/hooks/registry.json` is import output, not an `install-all` resource. |

For skills, dotpack installs the full regular-file package rooted at the
source `SKILL.md` directory. Sibling files such as `references/*.md`,
`scripts/*`, and `assets/*` are copied to the same relative paths under the
host skill directory, recorded as manifest file claims, and removed on
uninstall when still owned by that install. Symlinks are rejected for skill
support files.

## Source Layouts

`install-all` defaults to the canonical `.agents` layout. When `--from` points
at a project root, dotpack discovers `.agents/skills`, `.agents/agents`,
`.agents/rules`, `.agents/commands`, `.agents/mcp-servers`, and
`.agents/hooks`; when `--from` points directly at `.agents`, it discovers the
same kind directories inside that root.

Catalog repositories can override those discovery paths without changing the
resource schema, including repositories fetched directly from GitHub:

```sh
dotpack install-all --from /path/to/catalog --kind-path skill=skills
dotpack install-all --from /path/to/catalog --skills-path skills --agents-path agents
dotpack install-all --from github:BuilderIO/skills --skills-path skills --agent agents-cli --scope user
dotpack install-all --from github:OWNER/REPO@REF --kind-path skill=skills
```

Supported custom layout flags are `--kind-path kind=path` for `skill`,
`agent`, `rule`, `command`, `mcp-server`, and `hook`, plus the per-kind aliases
`--skills-path`, `--agents-path`, `--rules-path`, `--commands-path`,
`--mcp-servers-path`, and `--hooks-path`. Custom paths are relative to `--from`
unless absolute. Unspecified kinds keep the `.agents/<kind-dir>` default under
the source root. GitHub sources support `github:OWNER/REPO`,
`github:OWNER/REPO@REF`, and `https://github.com/OWNER/REPO`; cached checkouts
live under `DOTPACK_DOTPACK_HOME/cache/github`.

## Manifest Provenance

dotpack stores install provenance at `~/.dotpack/installs.yaml`, or under
`DOTPACK_DOTPACK_HOME` when that environment variable is set.

Manifest records include source paths and hashes, canonical and target roots,
target host, kind, scope, file claims, and merged config keys. dotpack removes
only files and config keys it can prove it wrote.

## Reconciliation Workflow

For a project with a canonical `.agents` tree:

```sh
CANONICAL=/path/to/project/.agents
TARGET=/path/to/project

dotpack inventory --from "$CANONICAL" --target "$TARGET" --agent agents-cli
dotpack sync-back --from "$CANONICAL" --target "$TARGET" --force
dotpack reset-materialized --from "$CANONICAL" --target "$TARGET" --include-untracked
dotpack install-all --from "$CANONICAL" --target "$TARGET" --agent agents-cli --scope project
```

`sync-back` copies file-drop resources only: skills, Markdown agents, rules, and
commands. Config-fragment sync-back from settings/config files remains an
importer concern because dotpack cannot prove ownership of arbitrary untracked
merged settings.

## Optional Lifecycle Hardening

By default, `install` and `install-all` only materialize host files. They do not
run post-install lifecycle hooks.

Teams that use Sponsio can opt into the bundled post-install verification task:

```sh
dotpack install .agents/hooks/bash-guard.hook.json --kind hook --agent agents-cli --scope project --run-lifecycle
dotpack install-all --from .agents --target . --agent agents-cli --scope project --run-lifecycle
```

When `--run-lifecycle` is set, dotpack expects `sponsio` to be installed on
`PATH` or provided through `DOTPACK_SPONSIO_BINARY`. The lifecycle task installs
Sponsio host wiring in observe mode and fails closed if verification fails.

## Paths And Environment

| Variable | Purpose |
| --- | --- |
| `DOTPACK_USER_HOME` | Overrides the user home base used by path resolution. |
| `DOTPACK_PROJECT_HOME` | Overrides the project root for project-scope installs and default reconciliation targets. |
| `DOTPACK_CLAUDE_HOME` | Overrides the Claude Code user home, default `~/.claude`. |
| `DOTPACK_GEMINI_HOME` | Overrides the Gemini CLI user home, default `~/.gemini`. |
| `DOTPACK_ANTIGRAVITY_HOME` | Overrides the Antigravity CLI user home, default `~/.antigravity`. |
| `DOTPACK_AGENTS_HOME` | Overrides the shared `.agents` user home, default `~/.agents`. |
| `DOTPACK_CODEX_HOME` | Overrides the Codex user home, default `~/.codex`. |
| `DOTPACK_OPENCODE_HOME` | Overrides the OpenCode user home, default `~/.config/opencode`. |
| `DOTPACK_DOTPACK_HOME` | Overrides dotpack state, default `~/.dotpack`. |
| `DOTPACK_SPONSIO_BINARY` | Overrides the Sponsio binary used by optional lifecycle verification. |

## Development

Install docs dependencies:

```sh
pip3 install -r docs/requirements.txt
```

Run the broad local checks:

```sh
gofmt -w $(git ls-files '*.go')
go mod tidy
go vet ./...
go test ./...
go build ./cmd/dotpack
mkdocs build --strict --site-dir /tmp/dotpack-site
gitleaks detect --source . --no-banner --redact
```

## License

dotpack is licensed under the Apache License 2.0. See [LICENSE](LICENSE).
