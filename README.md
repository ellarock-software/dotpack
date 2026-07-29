# dotpack

[![CI](https://github.com/ellarock-software/dotpack/actions/workflows/ci.yml/badge.svg)](https://github.com/ellarock-software/dotpack/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/ellarock-software/dotpack.svg)](https://pkg.go.dev/github.com/ellarock-software/dotpack)
[![Go Report Card](https://goreportcard.com/badge/github.com/ellarock-software/dotpack)](https://goreportcard.com/report/github.com/ellarock-software/dotpack)
[![Release](https://img.shields.io/github/v/release/ellarock-software/dotpack?sort=semver)](https://github.com/ellarock-software/dotpack/releases)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

dotpack is a Go CLI for portable AI-agent configuration. It validates
canonical `.agents` resources, installs them into host-native files, and
records ownership so installs can be listed, uninstalled, reconciled, reset,
and reinstalled safely.

## Why dotpack exists

Agent tools reuse the same ideas but package them differently: skills, agents,
rules, commands, memory files, MCP servers, and hooks are usually tied to one
tool's filesystem layout and config format. dotpack gives those resources a
portable `.agents` source shape and translates that source into host-native
output for the supported adapters.

```text
canonical .agents -> dotpack install/install-all -> host-native files
host-native files -> dotpack import/sync-back -> canonical .agents
```

## Status

dotpack's product intent is universal coverage across LLM coding tools and
operations. The current release ships adapters for `claude-code`,
`gemini-cli`, `antigravity-cli`, `codex`, `opencode`, `hermes`, and the
`agents-cli` umbrella target. The `import` command currently supports Claude
Code input.

## Install

With Go:

```sh
go install github.com/ellarock-software/dotpack/cmd/dotpack@latest
```

From source:

```sh
git clone https://github.com/ellarock-software/dotpack.git
cd dotpack
go build -o ./dotpack ./cmd/dotpack
./dotpack --help
```

## Quick Start

Assume a portable catalog like:

```text
/path/to/catalog/.agents/
  skills/reviewer/SKILL.md
  mcp-servers/github.mcp.json
  hooks/bash-guard.hook.json
```

Then install a single resource:

```sh
CATALOG=/path/to/catalog/.agents
TARGET=/path/to/project

dotpack install "$CATALOG/skills/reviewer/SKILL.md" --agent claude-code --scope project
dotpack install "$CATALOG/mcp-servers/github.mcp.json" --kind mcp-server --agent codex --scope user
dotpack install "$CATALOG/hooks/bash-guard.hook.json" --kind hook --agent agents-cli --scope project
```

Install a full canonical tree into a target project:

```sh
dotpack install-all --from "$CATALOG" --target "$TARGET" --agent agents-cli --scope project
```

Install from a repository with a non-canonical layout:

```sh
dotpack install-all --from /path/to/catalog --skills-path skills --agent agents-cli --scope user
dotpack install-all --from /path/to/catalog --kind-path skill=skills --kind-path agent=agents --agent claude-code
dotpack install-all --from github:BuilderIO/skills --skills-path skills --agent agents-cli --scope user
dotpack install-all --from github:OWNER/REPO@REF --kind-path skill=skills
```

Import a Claude Code tree back into canonical `.agents`:

```sh
dotpack import claude-code "$TARGET" --out "$TARGET"
```

Inspect or baseline the automatic skill gate:

```sh
BASELINES=/path/to/project/.dotpack/skillspector/baselines

dotpack baseline-skills "$CATALOG" --baseline-dir "$BASELINES"
dotpack scan-skills "$CATALOG" --baseline-dir "$BASELINES"
dotpack scan-skills /path/to/catalog --skills-path skills --format sarif --output /tmp/skills.sarif
```

`agents-cli` is an umbrella target, not a separate runtime. It preserves the
user-typed `agents-cli` identity in the manifest while fanning out to the
compatible sub-adapters.

## Commands

| Command | Purpose |
| --- | --- |
| `dotpack install <source-path>` | Install one portable resource into one host or umbrella target; skill installs run a mandatory static SkillSpector gate first. |
| `dotpack install-all` | Discover and install supported direct resources from a canonical `.agents` tree or explicit source layout; discovered skills are gated with SkillSpector first. |
| `dotpack scan-skills [source]` | Run static SkillSpector scans against one skill, a canonical `.agents` tree, or a custom skill root. |
| `dotpack baseline-skills [source]` | Generate one SkillSpector baseline YAML file per selected skill. |
| `dotpack inventory` | Classify materialized host file outputs against manifest claims and optional canonical output. |
| `dotpack sync-back` | Copy drifted or untracked materialized file-drop output back into canonical `.agents`. |
| `dotpack reset-materialized` | Remove materialized host output owned by dotpack for a target; optionally remove scanned untracked file-drop output. |
| `dotpack import` | Convert a native host tree into canonical `.agents`; currently supports Claude Code input. |
| `dotpack list` | List installed manifest records in stable manifest order. |
| `dotpack uninstall <name-or-id>` | Remove one installed resource by full ID or short name plus `--agent` and `--kind`; duplicate project IDs resolve from CWD or `--target`. |
| `dotpack reconcile` | Read-only manifest drift report for missing files, changed file hashes, or missing merged keys. |
| `dotpack prune` | Remove manifest records whose recorded claims are all absent from disk. |
| `dotpack version` | Print the dotpack version. |

## Current Host Coverage

**Product intent.** dotpack is designed for universal coverage across
operations (`skill`, `agent`, `rule`, `command`, `memory`, `mcp-server`,
`hook`). The architecture stays open by making hosts self-contained adapters
and config formats self-contained merge backends. See
[ADR-0014](docs/adr/0014-open-adapter-and-merge-backend-registries.md) and
[CONTRIBUTING.md](CONTRIBUTING.md).

**Current shipped adapters.** The matrix below is the set implemented today,
not the scope boundary of the project. Unsupported cells mean the host lacks a
native concept for that operation.

Project-scope paths are shown below. User-scope paths resolve under host home
directories such as `~/.claude`, `~/.gemini`, `~/.antigravity`, `~/.agents`,
`~/.codex`, `~/.config/opencode`, and `~/.hermes`.

| Kind | `claude-code` | `gemini-cli` | `antigravity-cli` | `codex` | `opencode` | `hermes` | `agents-cli` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `skill` | `.claude/skills/<name>/SKILL.md` | `.gemini/skills/<name>/SKILL.md` | `.antigravity/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` | `.opencode/skills/<name>/SKILL.md` | `~/.hermes/skills/<name>/SKILL.md` (user only) | writes `.agents/skills/<name>/SKILL.md` once |
| `agent` | `.claude/agents/<name>.md` | `.gemini/agents/<name>.md` | `.antigravity/agents/<name>.md` | `.codex/agents/<name>.toml` | `.opencode/agents/<name>.md` | unsupported | fans out to Gemini, Antigravity, and Codex |
| `rule` | `.claude/rules/<name>.md` | `.gemini/rules/<name>.md` | `.antigravity/rules/<name>.md` | `.codex/rules/<name>.md` | unsupported | unsupported | fans out to Gemini, Antigravity, and Codex |
| `command` | `.claude/commands/<name>.md` | `.gemini/commands/<name>.toml` | `.antigravity/commands/<name>.md` | `.codex/commands/<name>.md` | `.opencode/commands/<name>.md` | unsupported | fans out to each sub-adapter's command file |
| `memory` | `CLAUDE.md` | `GEMINI.md` | `ANTIGRAVITY.md` | `AGENTS.md` | `AGENTS.md` | `SOUL.md` (user) or `.hermes.md` / `HERMES.md` / `AGENTS.md` / `CLAUDE.md` (project) | fans out to each sub-adapter's memory file |
| `mcp-server` | `.mcp.json` or `~/.claude.json` | `.gemini/settings.json` | `.antigravity/settings.json` | `.codex/config.toml` | `opencode.json` (`$.mcp`) | `~/.hermes/config.yaml` (`mcp_servers`) | fans out to Gemini, Antigravity, and Codex |
| `hook` | `.claude/settings.json` | `.gemini/settings.json` | `.antigravity/settings.json` | `.codex/config.toml` | unsupported | `~/.hermes/config.yaml` (`hooks`) | fans out to Gemini, Antigravity, and Codex |

Merged config writes (`mcp-server`, `hook`) go through a pluggable backend
keyed by file extension. `.json`, `.toml`, and `.yaml`/`.yml` ship today.

## Canonical Resource Shapes

The schemas live in `schema/*.yaml`; parsers live under `internal/resource`.

| Kind | Canonical shape |
| --- | --- |
| `skill` | A directory containing `SKILL.md` with frontmatter and Markdown body. `--kind` is inferred when the source file is named `SKILL.md`. |
| `agent` | A Markdown file with frontmatter and body, normally `.agents/agents/<name>.md`. Direct install requires `--kind agent`. |
| `rule` | A Markdown file at `.agents/rules/<name>.md` with YAML frontmatter containing `id` or `name` and a Markdown body. Direct `.agents/rules/*.md` installs infer `--kind rule`. |
| `command` | A Markdown command with YAML frontmatter and body, or a TOML command with a `prompt` field. Direct `.agents/commands/*.md|.toml` installs infer `--kind command`. |
| `memory` | A whole Markdown memory file such as `CLAUDE.md`, `GEMINI.md`, `AGENTS.md`, `ANTIGRAVITY.md`, `HERMES.md`, `.hermes.md`, or `SOUL.md`; no frontmatter is required. |
| `mcp-server` | A JSON fragment shaped as `{"mcpServers": {"<name>": {...}}}`. Direct install requires `--kind mcp-server`. |
| `hook` | A JSON fragment shaped around a top-level `hooks` map. The source filename supplies the install name. `.agents/hooks/registry.json` is import output, not an `install-all` resource. |

For skills, dotpack installs the full regular-file package rooted at the source
`SKILL.md` directory. Sibling files such as `references/*.md`, `scripts/*`, and
`assets/*` are copied to the same relative paths under the host skill
directory, recorded as manifest file claims, and removed on uninstall when
still owned by that install. Symlinks are rejected for skill support files.

Source-backed skill packages preserve `SKILL.md` byte-for-byte, including when
an install requires `--allow-lossy`. The flag acknowledges that the target host
cannot honor one or more source fields; it does not authorize dotpack to mutate
the package. This distinction keeps package-local signatures, seals, and
provenance references valid. Skills synthesized in memory without source bytes
are encoded into the target host's supported shape.

## Flexible Source Layouts

`install-all` defaults to the canonical `.agents` layout. When `--from` points
at a project root, dotpack discovers `.agents/skills`, `.agents/agents`,
`.agents/rules`, `.agents/commands`, `.agents/mcp-servers`, and `.agents/hooks`;
when `--from` points directly at `.agents`, it discovers the same kind
directories inside that root.

Custom layout flags are `--kind-path kind=path` for `skill`, `agent`, `rule`,
`command`, `mcp-server`, and `hook`, plus the per-kind aliases
`--skills-path`, `--agents-path`, `--rules-path`, `--commands-path`,
`--mcp-servers-path`, and `--hooks-path`. Custom paths are relative to
`--from` unless absolute. Unspecified kinds keep the `.agents/<kind-dir>`
default under the source root.

GitHub sources support `github:OWNER/REPO`, `github:OWNER/REPO@REF`, and
`https://github.com/OWNER/REPO`. Cached checkouts live under
`DOTPACK_DOTPACK_HOME/cache/github`.

## Manifest And Reconciliation

dotpack stores install provenance at `~/.dotpack/installs.yaml`, or under
`DOTPACK_DOTPACK_HOME` when that environment variable is set.

Manifest records include source paths and hashes, canonical and target roots,
target host, kind, scope, file claims, and merged config keys. dotpack removes
only files and config keys it can prove it wrote.

Reinstalling the same resource ID into the same target root reconciles
dotpack-owned file output to the new plan: files claimed by the prior record
but absent from the new source are removed. Untracked files are not removed.

`list` intentionally reads the global manifest and shows records from every
target root. `reconcile` is provenance-driven: it checks recorded claims for
the current project but does not scan for unclaimed files. Use `inventory` when
you need tracked, drifted, missing, and untracked filesystem classification.

For a project with a canonical `.agents` tree:

```sh
CANONICAL=/path/to/project/.agents
TARGET=/path/to/project

dotpack inventory --from "$CANONICAL" --target "$TARGET" --agent agents-cli
dotpack sync-back --from "$CANONICAL" --target "$TARGET" --force
dotpack reset-materialized --from "$CANONICAL" --target "$TARGET" --include-untracked
dotpack install-all --from "$CANONICAL" --target "$TARGET" --agent agents-cli --scope project
```

`sync-back` copies file-drop resources only: skills, Markdown agents, rules,
and commands. Config-fragment sync-back from settings/config files remains an
importer concern because dotpack cannot prove ownership of arbitrary untracked
merged settings.

## SkillSpector Gating

dotpack ships a native SkillSpector integration for skill packages.

- Skill-bearing workflows automatically run a static SkillSpector gate before
  dotpack reads or materializes skill content. This covers `install`,
  `install-all`, canonical `inventory` comparisons, Claude Code `import`, and
  `sync-back` when skills are involved.
- `scan-skills` is static-only by default and gates by default: unsuppressed
  findings return a non-zero exit code unless you pass `--report-only`.
- Every skill-bearing command accepts repeatable
  `--skill-bypass-security <name>` arguments when a named skill must not be
  scanned. Bypasses are invocation-local, exact-name matched, prominently
  reported, and fail closed when a requested name is not selected.
- `baseline-skills` writes per-skill baseline files as
  `<baseline-dir>/<skill-name>.yaml`.
- Automatic skill gates look for baselines at
  `<policy-root>/.dotpack/skillspector/baselines`. For canonical `.agents` or
  host-native roots such as `.claude`, `policy-root` is the parent project
  directory.
- `scan-skills` accepts a direct `SKILL.md`, a skill directory, a canonical
  `.agents` tree, a project containing `.agents`, or a custom skill root via
  `--skills-path` / `--kind-path skill=...`.
- Runtime provisioning is dotpack-owned and generic. dotpack installs
  SkillSpector under `DOTPACK_DOTPACK_HOME/skillspector`, pins the upstream repo
  `NVIDIA/SkillSpector` to commit
  `ac6b41b7a28b7b3d9001e43fbbf710d4267d5a7c`, and records runtime metadata in
  `DOTPACK_DOTPACK_HOME/skillspector/runtime.json`.

Examples:

```sh
CATALOG=/path/to/project/.agents
BASELINES=/path/to/project/.dotpack/skillspector/baselines

dotpack baseline-skills "$CATALOG" --baseline-dir "$BASELINES"
dotpack scan-skills "$CATALOG" --baseline-dir "$BASELINES"
dotpack scan-skills "$CATALOG" --changed --base origin/main --skill reviewer
dotpack scan-skills "$CATALOG" --skill-bypass-security legacy-skill
dotpack scan-skills /path/to/catalog --skills-path skills --format json --output /tmp/skills.json
```

Use `scan-skills` when you want to inspect or export the same findings
directly, and `baseline-skills` when you need to author reviewed suppressions
for the automatic gate. Prefer a baseline when a skill can still be scanned:
baselines suppress specific reviewed findings, while
`--skill-bypass-security` prevents SkillSpector from scanning the named skill.

## Optional Lifecycle Verification

By default, `install` and `install-all` only materialize host files. They do
not run post-install lifecycle hooks.

Teams that use Sponsio can opt into the bundled post-install verification task.
(Sponsio is an Ella Rock Software tool and is not yet publicly available; this
integration is a reference example of dotpack's optional lifecycle mechanism and
is never required.)

```sh
dotpack install "$CATALOG/hooks/bash-guard.hook.json" --kind hook --agent agents-cli --scope project --run-lifecycle
dotpack install-all --from "$CATALOG" --target "$TARGET" --agent agents-cli --scope project --run-lifecycle
```

When `--run-lifecycle` is set, dotpack expects `sponsio` to be installed on
`PATH` or provided through `DOTPACK_SPONSIO_BINARY`. The lifecycle task
installs Sponsio host wiring in observe mode and fails closed if verification
fails.

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
| `DOTPACK_HERMES_HOME` | Overrides the Hermes user home, default `~/.hermes`. |
| `DOTPACK_DOTPACK_HOME` | Overrides dotpack state, default `~/.dotpack`. |
| `DOTPACK_DOTPACK_HOME/skillspector` | Default root for the pinned SkillSpector runtime, cached run artifacts, and runtime metadata. |
| `DOTPACK_SPONSIO_BINARY` | Overrides the Sponsio binary used by optional lifecycle verification. |

## Documentation

- [Contributing guide](CONTRIBUTING.md)
- [Roadmap](ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Docs index](docs/index.md)
- [Schema reference](docs/schemas/skill.md)
- [SkillSpector scanning guide](docs/SKILLSPECTOR_SCAN_INSTRUCTIONS.md)
- [Architecture decisions](docs/adr/0015-native-skillspector-skill-scanning.md)
- [Security policy](SECURITY.md)

## Development

Install docs dependencies:

```sh
pip3 install -r docs/requirements.txt
```

Run the same broad checks as CI:

```sh
gofmt -w $(git ls-files '*.go')
go mod tidy
go vet ./...
go test ./...
go build ./cmd/dotpack
mkdocs build --strict --site-dir /tmp/dotpack-site
gitleaks git . --config .gitleaks.toml --redact --no-banner
```

## License

dotpack is licensed under the Apache License 2.0. See [LICENSE](LICENSE).
