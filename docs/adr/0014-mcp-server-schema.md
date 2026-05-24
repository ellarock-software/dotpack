# MCP-server schema: per-entry counting, silent-ignore footguns, transport divergence

## Context

Per [ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md). Sixth kind surveyed; **re-surveyed with Codex corpus after `scripts/survey.sh` TOML extraction landed** (task #14). The `mcp-server` kind is a config-fragment shape (like `hook`), but with a different structural pattern: a top-level map keyed by *server name*, each value being a server-entry object. The schema describes the value-object, not the whole map.

Corpus: 3 `.mcp.json` files (Claude project-scope) containing 16 server entries + 3 `.codex/config.toml` files (Codex) containing 9 server entries = **6 files, 25 entries total**.

## Decision

`schema/mcp-server.yaml` describes a single server entry with 4 fields, expressing a discriminated stdio-vs-HTTP transport:

- `command` (20/25 entries) — required when stdio transport (no `url`)
- `args` (20/25) — required when `command` is present
- `url` (5/25) — required when HTTP transport (no `command`)
- `env` (8/25) — optional environment variables

The schema also documents three host-specific install-target footguns and the Codex superset field set (kept as adapter pass-through, not promoted).

## Why

**Codex MCP-server schema is a SUPERSET, not a subset.** Codex spec (developers.openai.com/codex/config-reference) defines 18+ fields beyond the cross-host common core: per-tool approval overrides, allowlists, env-var indirection (`bearer_token_env_var`, `env_vars` with source = local|remote), HTTP-transport headers, OAuth scopes/resource, dual-unit startup timeouts (`startup_timeout_sec` + `_ms` alias), tool-call timeouts, experimental remote-executor placement, and a per-server `enabled`/`required` lifecycle pair. None of these clear the 2-entry floor in the cross-host corpus. **Schema stays narrow per the established methodology (narrow universal core + adapter pass-through extensions)**, NOT a union. The Codex adapter preserves these fields on round-trip; per-instance lossy detection (see ADR-0007 addendum) flags installs that drop them via non-Codex adapters.

**Transport discrimination by field presence, not by `type`.** Codex spec uses presence of `url` vs `command` as the transport discriminator. Corpus has both `type = "stdio"` (8 ChrisWiles Claude entries) and `type = "http"` (5 arc-kit Codex entries) — but the latter is a Claude-style holdover by the author, NOT a Codex spec field (Codex ignores it). The schema follows the spec: no `type` field; transport is inferred. Translator drops `type` on Codex import; passes through for Claude/Gemini where it may carry meaning.

**Per-entry counting beats per-file counting.** Pre-existing methodology (from original ADR), reaffirmed by Codex: per-entry granularity surfaces field adoption that per-file counting hides. `env` appears in 8/25 entries across 4 files (was 7/16 across 2 files in Claude-only corpus). Nested sub-entries (wp-calypso's `tools.<toolname>.approval_mode`) do NOT inherit server-entry granularity — they're sub-floor at the server-entry level (1/25 has the `tools` field).

**The `headers` vs `http_headers` ambiguity is documented, not resolved.** arc-kit uses `headers = {...}` for API-key injection (2/25 entries); Codex spec field is `http_headers` (0/25). Either arc-kit is wrong (Codex silently ignores), `headers` is an undocumented alias, or the spec lags. Neither field promoted to schema; translator canonicalises on import to Codex (rewrite `headers` → `http_headers`, preserve original spelling in resource metadata for traceability).

**The silent-ignore Claude footgun is load-bearing.** Adapters that write to the wrong file produce installs that *appear* to succeed but don't take effect. The user sees no MCP server show up in their Claude Code session and has no error to debug. Worst-case failure mode. The schema documents the correct files (`.mcp.json`, `~/.claude.json`) and the schema test suite (future) should include an assertion that the Claude adapter never writes mcpServers into settings.json.

**Credential-in-args detection is a security-agent concern.** abcdan's `--figma-api-key=XXXXXXXX` shows a real pattern: credentials embedded in `args` rather than `env`. arc-kit's `headers = { "X-API-Key" = "${...}" }` uses env-var indirection. Codex spec's `bearer_token_env_var` is the cleanest pattern. ADR-0001's security-agent stage should flag high-entropy args during translation review. Documented in ecosystem_notes; no schema-level enforcement.

**Per-tool approval (wp-calypso's `tools.click.approval_mode`) carries security semantics.** Below the corpus floor at server-entry granularity (1/25), but the field encodes a security control — per-tool approval modes are how Codex users gate destructive tools. Adapter pass-through is fine for Codex round-trip; per-instance lossy detection (ADR-0007) must trigger when porting such a resource to a host that lacks the concept — silently dropping a security control is the kind of failure dotpack exists to prevent.

## Consequences

**Manifest schema for mcp-server installs.** Per [ADR-0008](./0008-manifest-as-install-provenance-source-of-truth.md), `merged_keys` records the keys this resource added. For mcp-server that's `$.mcpServers.<server-name>` (Claude/Gemini) or `mcp_servers.<server-name>` (Codex) — a single key per install. Name collision between two installs into the same target file must fail fast (per ecosystem_notes); the manifest's collision detection check is the place to enforce this.

**Adapter capability matrix.**
- `claude-code`: native. Writes to `.mcp.json` (project) or `~/.claude.json` (user). Refuses settings.json for mcpServers. Manifest tracks merged keys. Lossy when source carries Codex-superset fields (per ADR-0007 addendum).
- `agents-cli/gemini`: native. Writes to `.gemini/settings.json $.mcpServers`. Supports the four universal fields; extension fields (httpUrl, cwd, timeout, trust) pass-through-if-present.
- `agents-cli/codex`: **native (upgraded from unsupported)**. Writes to `~/.codex/config.toml mcp_servers` (snake_case). Full Codex superset preserved on round-trip. Lossy never triggers when source IS a Codex resource.

**Task #4 (agents-cli fan-out) now has its full requirement set.** Across the fragment kinds (hook, mcp-server) plus the file-placement question for skill/agent/command, the agents-cli adapter must:
- Decide JSON vs TOML format per host (`yq -p toml -o json` pattern from task #14 reusable).
- Decide field-name canonicalisation (`mcpServers` ↔ `mcp_servers`; PreToolUse ↔ BeforeTool; `headers` → `http_headers`).
- Decide unit conversion (timeout seconds vs ms for hook; `startup_timeout_sec` vs `_ms` within Codex).
- Track per-host merged keys in manifest for surgical uninstall.
- Trigger per-instance lossy detection (ADR-0007 addendum) when a Codex-rich resource is installed via a thinner adapter.

**Phase 0 complete (for real this time).** All six MVP kinds (skill, agent, command, memory, hook, mcp-server) have schemas at `schema/<kind>.yaml`, ADRs at `docs/adr/0009-...0014-...md`, and **Codex coverage across the two fragment kinds**. The TOML-extraction blocker (`scripts/survey.sh` TODO) is closed.

## Artefacts

- `schema/mcp-server.yaml` — per-entry schema, transport discrimination, Codex superset documented
- `schema-corpus.yaml` (kind: mcp-server) — 3 Claude JSON + 3 Codex TOML, 25 server entries
- `scripts/survey.sh` — TOML extraction via `yq -p toml -o json`
- `.dotpack-workdirs/survey/mcp-server/`
