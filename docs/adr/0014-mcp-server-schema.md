# MCP-server schema: per-entry counting, silent-ignore footguns, transport divergence

## Context

Per [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md). Sixth and final kind surveyed. The `mcp-server` kind is a config-fragment shape (like `hook`), but with a different structural pattern: a top-level map keyed by *server name*, each value being a server-entry object. The schema describes the value-object, not the whole map.

Corpus: 3 `.mcp.json` files (Claude project-scope) containing **16 server entries** total. No real-world Gemini `.gemini/settings.json` with `mcpServers` was findable in the survey window; Codex hooks/MCP need TOML extraction support which is still TODO in `scripts/survey.sh`.

## Decision

`schema/mcp-server.yaml` describes a single server entry with 4 fields:

- `command` (required, 16/16) — executable.
- `args` (required, 16/16) — argument array.
- `type` (optional, 8/16) — transport identifier; only "stdio" observed.
- `env` (optional, 7/16) — credential / config env-var map.

Three deliberate exclusions documented: Gemini-spec fields (`cwd`, `timeout`, `trust`, `httpUrl`, `url`, `headers`) that are documented but absent from corpus.

The schema also documents three host-specific install-target footguns:
- Claude: `.mcp.json` is the project location; `~/.claude.json` is user. `mcpServers` in `.claude/settings.json` is **silently ignored** — a known footgun (anthropics/claude-code#24477).
- Gemini: `.gemini/settings.json` honours `mcpServers`.
- Codex: `~/.codex/config.toml mcp_servers`; unsupported pending TOML extraction.

## Why

**Per-entry counting beats per-file counting.** The survey reported per-file counts (`type: 1/3 files`, `env: 1/3 files`) which misrepresented adoption. There are 16 server entries across 3 files; counted that way, `type: 8/16` (50%) and `env: 7/16` (44%). One file contributes 8 highly-uniform entries (ChrisWiles, all SaaS integrations), another contributes 5 entries with diverse transport choices (abcdan), and the third contributes 3 minimal entries (shanraisshan). Per-file counting hides the per-entry truth. This is the methodology insight specific to this kind: when the fragment is a map of homogeneous entries, count entries, not files. Worth adding to the survey prompt before any re-runs.

**Why `type` is optional despite 8/16.** Half-presence in this corpus, but the observed half is all `"stdio"` — the host default. Marking it required would force translators to add `type: "stdio"` to every entry, increasing config noise with no semantic value. Hosts that introduce non-stdio transports (Gemini's `httpUrl`/`sse`) signal them via *other* fields, not by setting `type` differently. Keep optional.

**Why `args` is required even when an entry has no flags.** Every corpus entry has `args` set (some to a one-element array, never absent). Tooling that constructs entries without `args` would emit malformed entries that hosts may reject — the field is structurally expected even if conceptually empty. Required.

**The silent-ignore Claude footgun is load-bearing.** Adapters that write to the wrong file produce installs that *appear* to succeed but don't take effect. The user sees no MCP server show up in their Claude Code session and has no error to debug. This is the worst possible failure mode. The schema documents the correct files (`.mcp.json`, `~/.claude.json`) and the schema test suite (future) should include an assertion that the Claude adapter never writes mcpServers into settings.json.

**Credential-in-args detection is a security-agent concern.** abcdan's `--figma-api-key=XXXXXXXX` shows a real pattern: credentials embedded in `args` rather than `env`. ADR-0001's security-agent stage should flag high-entropy args during translation review. Documented in ecosystem_notes; no schema-level enforcement (legitimate args contain high-entropy values too — paths, UUIDs, etc.).

**Remote-MCP wrapper detection.** abcdan's Atlassian entry is structurally `command: npx args: [-y, mcp-remote, https://mcp.atlassian.com/v1/sse]` — a stdio wrapper around an SSE endpoint. Gemini's spec has native `httpUrl`/`url`/`headers` fields that express the same. The translator detecting `mcp-remote` (or similar shims) and re-emitting native HTTP form when targeting a host that supports it would be cleaner than literal-stdio round-tripping. Not a v1 requirement; documented.

**Gemini corpus gap.** Searches for in-the-wild `.gemini/settings.json` with `mcpServers` returned only docs-pages and issue threads, no large public configs. The Gemini-spec extension fields (`httpUrl`, `headers`, `cwd`, `timeout`, `trust`) are documented at github/github-mcp-server's install-gemini-cli.md but no corpus presence. Acknowledged limitation; re-run the survey when the Gemini ecosystem matures.

## Consequences

**Manifest schema for mcp-server installs.** Per [ADR-0008](./0008-manifest-as-install-provenance-source-of-truth.md), `merged_keys` records the keys this resource added. For mcp-server that's `$.mcpServers.<server-name>` — a single key per install. Name collision between two installs into the same target file must fail fast (per ecosystem_notes); the manifest's collision detection check is the place to enforce this.

**Adapter capability matrix.**
- `claude-code`: native. Writes to `.mcp.json` (project) or `~/.claude.json` (user). Refuses settings.json for mcpServers (the footgun). Manifest tracks merged keys.
- `agents-cli/gemini`: native. Writes to `.gemini/settings.json` `$.mcpServers`. Supports the four universal fields; extension fields (`httpUrl`, `cwd`, `timeout`, `trust`) are pass-through-if-present.
- `agents-cli/codex`: unsupported pending TOML extraction in survey.sh AND in dotpack's TOML write support. Default-deny.

**Task #4 (agents-cli fan-out) now has its full requirement set.** Across the three fragment kinds (hook, mcp-server, and the still-TBD file-placement for skill/agent/command into the right `~/.agents/...` subdirectory), the agents-cli adapter must:
- Decide JSON vs TOML format per host (extraction blocker for Codex).
- Decide field-name canonicalisation (PreToolUse ↔ BeforeTool for hook; mcpServers vs mcp_servers for Codex).
- Decide unit conversion (timeout seconds vs ms for hook).
- Track per-host merged keys in manifest for surgical uninstall.

**Phase 0 complete.** All six MVP kinds (skill, agent, command, memory, hook, mcp-server) have schemas at `schema/<kind>.yaml` and ADRs at `docs/adr/0009-...0014-...md`. Methodology refinements landed in `scripts/survey.sh` between kinds. Task #3 closes; downstream code work (tasks #5, #6, #4) can begin.

## Artefacts

- `schema/mcp-server.yaml` — per-entry schema, install-target footguns documented
- `schema-corpus.yaml` (kind: mcp-server) — 3 files, 16 server entries
- `.dotpack-workdirs/survey/mcp-server/`
