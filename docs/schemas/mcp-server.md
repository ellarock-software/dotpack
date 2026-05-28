# Mcp-Server Schema

**Version:** `0`

## Template

- **Shape:** `config_fragment`
- **Filename:** ``

## Ecosystem Notes

- Codex MCP-server schema is a SUPERSET of Claude/Gemini. The full Codex spec adds (per developers.openai.com/codex/config-reference): cwd, enabled, required, env_vars (with source = local|remote), bearer_token_env_var, env_http_headers, http_headers, oauth_resource, scopes, startup_timeout_sec (+ _ms alias), tool_timeout_sec, default_tools_approval_mode, enabled_tools / disabled_tools (allow/ deny lists), experimental_environment (local|remote), and per-tool overrides via tools.<toolname>.approval_mode. None of these clear the universal-schema floor (all <= 1 file in corpus); they are adapter pass-through fields for the Codex adapter, NOT promoted to the cross-host schema. See [ADR-0003](../adr/0003-universal-kinds-with-adapter-capability-matrix.md) consequences re: per-instance lossy promotion when a Codex resource carrying these fields is installed via a non-Codex adapter.
- `type` field NOT promoted to schema despite 13/25 corpus presence (8 'stdio' from ChrisWiles, 5 'http' from arc-kit). Reason: Codex spec discriminates transport by PRESENCE of `url` vs `command`, not by a `type` field. arc-kit's `type = 'http'` is a Claude-style holdover by the author; Codex parser ignores it. Schema uses the spec-canonical discriminator (url-vs-command). Translator drops `type` on import to Codex; passes through for Claude/Gemini where it may still carry meaning.
- `headers` vs `http_headers` (arc-kit): arc-kit uses `headers = {...}` for API-key injection; Codex spec field is `http_headers`. Either arc-kit is wrong (Codex silently ignores `headers`) or `headers` is an undocumented alias. Both seen in corpus (2/25). NEITHER promoted to schema until the spec/source confirms. Translator canonicalises on import: rewrite `headers` to `http_headers` when targeting Codex, flag the original spelling in resource metadata for traceability.
- Per-tool approval override (wp-calypso): chrome-devtools entry contains `tools.click.approval_mode = 'approve'` and `tools.evaluate_script.approval_mode = 'approve'`. Counting choice: these are SUB-entries of one server entry, not separate server entries. At server-entry granularity, 1/25 has the `tools` field — below floor. At nested-tool-entry granularity, 2/2 of wp-calypso's tools have approval_mode — but the denominator is meaningless across the corpus. Schema-wise: not promoted; adapter pass-through. Per [ADR-0001](../adr/0001-empirically-derived-schema-via-corpus-survey.md), may also be a security-agent input (per-tool approval is a security control — translator should not silently drop it when porting to a host that lacks the concept).
- Filename + key-name divergence: Claude `.mcp.json $.mcpServers` (camelCase), Gemini `.gemini/settings.json $.mcpServers` (camelCase), Codex `.codex/config.toml mcp_servers` (snake_case). Adapter translates the key name on emit; the schema's `template.source_locations` declares each canonical location.
- Claude footgun: `mcpServers` in `.claude/settings.json` is SILENTLY IGNORED by Claude Code (anthropics/claude-code#24477, #646). Adapters MUST write to `.mcp.json` or `~/.claude.json`.
- Argument-style credential handling varies. abcdan/mcp.json embeds secrets directly in args (`--figma-api-key=XXXXXXXX`). arc-kit uses `headers={X-API-Key=${...}}` with ${VAR} substitution. Codex spec `bearer_token_env_var` is the cleanest pattern (env-var indirection). Translator should detect literal secrets in args (high-entropy strings) and surface a warning per the security-agent stage ([ADR-0001](../adr/0001-empirically-derived-schema-via-corpus-survey.md)). No schema-level enforcement (legitimate args contain high-entropy values too — paths, UUIDs, etc.).
- Remote-MCP wrapper detection. abcdan's Atlassian entry is structurally `command: npx args: [-y, mcp-remote, https://...sse]` — a stdio wrapper around an SSE endpoint. arc-kit's HTTP entries express the same concept natively via `url`. Codex spec supports both. Translator detecting `mcp-remote` (or similar shims) and re-emitting native HTTP form when targeting a host that supports it is cleaner than literal-stdio round-tripping. Not v1; documented.
- Server name (the JSON/TOML key) is the install identifier. Two installs with the same name into the same target file collide — manifest must record the server-name claim so a second install fails fast rather than silently overwriting.

## Deliberately Excluded Concepts

### Concept: `transport_type_marker`

The `type` field (corpus: 8 "stdio" from ChrisWiles Claude entries,
5 "http" from arc-kit Codex entries) is NOT a real transport
discriminator on any host. Codex spec discriminates by presence of
`url` vs `command`. arc-kit's `type = "http"` is a Claude-style
holdover the Codex parser ignores. Claude and Gemini accept the
field but do not parse it semantically (corpus uses it as
documentation only). Adapter behaviour:
- All adapters: pass-through if present; never emit if absent.
- Never lossy (lossy_when_dropped: false) — no host's runtime
  depends on `type`. The validator rejects sources where `type`
  contradicts the transport discriminator (`type: "http"` without
  `url`, or `type: "stdio"` without `command`); see [ADR-0010](../adr/0010-mcp-server-schema.md) and
  [ADR-0012](../adr/0012-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md) §7.

**Field Names:** `type`

### Concept: `http_transport_headers`

HTTP transport custom headers. Codex spec field is `http_headers`;
arc-kit (2/25) uses `headers` — either an undocumented alias or
a typo Codex silently ignores. Both names listed in aliases under
host: codex because the translator preserves the source spelling
on import while canonicalising semantically (see
ecosystem_notes — translator rewrites `headers` → `http_headers`
when targeting Codex emit). Claude/Gemini have no HTTP-transport
custom-headers concept in spec (Gemini's `headers` extension is
undocumented and not corpus-attested). Adapter behaviour:
- codex adapter: emit as `http_headers` (canonical Codex name).
  If source has `headers`, treat as `http_headers` and warn that
  the spelling was normalised.
- non-codex adapters: surface as lossy. HTTP authentication is
  load-bearing; silently dropping headers means the install will
  fail at runtime with confusing auth errors.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `http_headers` |
| `codex` | `headers` |

### Concept: `stdio_working_directory`

Working directory for the stdio MCP server process. Same name
(`cwd`) across both Codex and Gemini specs; same semantics. Claude
has no documented `cwd` field — stdio servers inherit Claude's
working directory. Adapter behaviour:
- codex / gemini-cli adapters: emit natively when present.
- claude-code adapter: surface as lossy. The MCP server's behaviour
  depends on where it runs (e.g., a server reading config from a
  relative path); silently dropping `cwd` means the server may not
  find its config on Claude. Install proceeds only with
  `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `cwd` |
| `gemini-cli` | `cwd` |
| `antigravity-cli` | `cwd` |

### Concept: `gemini_server_overall_timeout`

Gemini-spec field for an overall server timeout (no corpus
presence). Distinct from Codex's per-phase timeouts
(startup_timeout_sec, tool_timeout_sec) — those are separate
canonical_concepts. Adapter behaviour:
- gemini-cli adapter: emit natively when present.
- non-gemini adapters: surface as lossy. Adapter ergonomics note:
  if a future ADR-0017 unifies overall-vs-phase timeouts into one
  canonical_concept, this entry merges with codex_startup_timeout
  and codex_tool_call_timeout. Deferred per [ADR-0012](../adr/0012-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md) (Scenario B).

**Aliases:**

| Host | Field Name |
| --- | --- |
| `gemini-cli` | `timeout` |
| `antigravity-cli` | `timeout` |

### Concept: `gemini_server_trust`

Gemini-spec field for marking a server as "trusted" (no corpus
presence). Encodes a security control — trusted servers bypass
certain approval prompts. Adapter behaviour:
- gemini-cli adapter: emit natively when present.
- non-gemini adapters: surface as lossy. Security control —
  silently dropping it means a server an author marked trusted
  on Gemini gets default-untrusted treatment on Claude/Codex,
  which may surprise the user. Install proceeds only with
  `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `gemini-cli` | `trust` |
| `antigravity-cli` | `trust` |

### Concept: `gemini_http_url_alias`

Gemini's documented name for HTTP transport URL. The universal
core promotes `url` (the Codex spec name, 5/25 corpus presence
from arc-kit) as the canonical HTTP-URL field. `httpUrl` is the
SAME CONCEPT under a different name — this is a Scenario B case
(cross-host concept-equivalence under different names) called
out in [ADR-0012](../adr/0012-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md) Consequences as deferred to ADR-0017. The
`canonicalises_to: url` annotation is the machine-readable anchor
ADR-0017 will consume.
For MVP:
- The translator on Gemini-source import should normalise `httpUrl`
  to the universal `url` field. (Translator concern, not adapter.)
- If the translator fails to normalise and the canonical resource
  carries `httpUrl` in Extensions, lossy detection per the table
  above flags it as Gemini-only — over-strict for installs on
  Codex (which supports the same concept under name `url`).
  Acceptable until ADR-0017 introduces alias-aware translation.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `gemini-cli` | `httpUrl` |
| `antigravity-cli` | `httpUrl` |

### Concept: `codex_server_lifecycle`

Codex-spec lifecycle flags: `enabled: false` disables a server
without removing it from config; `required: true` causes Codex
to fail startup if the server doesn't initialise (vs default
warn-and-continue). Adapter behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. `enabled: false` is
  particularly load-bearing — silently emitting the server as
  active on Claude/Gemini contradicts the author's explicit
  intent. Install proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `enabled` |
| `codex` | `required` |

### Concept: `codex_env_vars_with_source`

Codex-spec richer environment-variable declaration with
per-variable `source = local|remote` indirection (vs the
universal `env` field which is a flat `{name: value}` map).
Scenario B-adjacent: `env_vars` and the universal `env` overlap
in concept (env vars for the server process) but `env_vars`
encodes additional information (variable source). Adapter
behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. The `source = remote`
  case encodes that a credential lives in Codex's secret store,
  not in the local environment — dropping it means the variable
  is unset at server startup on other hosts, which silently
  breaks auth. Install proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `env_vars` |

### Concept: `codex_bearer_token_env_var`

Codex-spec field for HTTP-transport bearer auth: the name of an
env var whose value Codex injects as `Authorization: Bearer
${value}`. Cleanest credential pattern in the corpus (vs
args-embedded secrets at abcdan or template-interpolated headers
at arc-kit). Adapter behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. HTTP auth is load-bearing;
  dropping the bearer-token directive means the HTTP server
  rejects all requests with 401. Install proceeds only with
  `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `bearer_token_env_var` |

### Concept: `codex_http_auth_metadata`

Codex-spec HTTP-transport auth metadata: `env_http_headers`
(env-var-interpolated custom headers, vs the literal-values
http_headers above), `oauth_resource` (OAuth resource identifier),
`scopes` (OAuth scope list). Adapter behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. OAuth metadata is
  load-bearing; dropping it means token-acquisition fails. Install
  proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `env_http_headers` |
| `codex` | `oauth_resource` |
| `codex` | `scopes` |

### Concept: `codex_startup_timeout`

Codex-spec timeout for server startup phase (vs request handling).
Two unit-suffixed aliases for the same field — Codex spec
documents both, parser accepts either. Adapter behaviour:
- codex adapter: emit whichever the source carried; if both
  present, prefer `_sec` per Codex spec precedence. The
  validator warns on conflict.
- non-codex adapters: surface as lossy. Startup timeout encodes
  an explicit author-chosen wait-budget; silently dropping it
  means the host uses its default (which may be shorter for a
  slow-starting server, causing intermittent install-time
  failures). Install proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `startup_timeout_sec` |
| `codex` | `startup_timeout_ms` |

### Concept: `codex_tool_call_timeout`

Codex-spec timeout for individual tool calls (vs startup). Adapter
behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. Same rationale as
  codex_startup_timeout. Install proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `tool_timeout_sec` |

### Concept: `codex_default_tool_approval_mode`

Codex-spec server-wide approval default — gates whether tools
from this server require user approval. Security-relevant.
Adapter behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. Author explicitly chose
  an approval posture; silently dropping it means tools run
  under the host's default (which may be more permissive),
  bypassing the security control. Install proceeds only with
  `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `default_tools_approval_mode` |

### Concept: `codex_tool_allowlist`

Codex-spec per-server tool allowlist (`enabled_tools`) and
denylist (`disabled_tools`). Security control. Adapter behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. An author who restricted
  the server's exposed tools via these lists chose explicit
  attack-surface reduction; silently emitting the unrestricted
  server elsewhere exposes more tools than the author intended.
  Install proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `enabled_tools` |
| `codex` | `disabled_tools` |

### Concept: `codex_experimental_environment`

Codex-spec `experimental_environment: local|remote` for server
placement (whether Codex runs the server itself or expects a
remote executor). Adapter behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. `remote` placement
  means the server runs outside the host's process — emitting
  the entry on Claude/Gemini without the remote-executor
  infrastructure would attempt local launch and fail. Install
  proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `experimental_environment` |

### Concept: `per_tool_approval`

Codex-spec per-tool approval override (`tools.<id>.approval_mode`).
A security control — wp-calypso uses it to require user approval
for chrome-devtools' `click` and `evaluate_script` tools, which
can perform destructive actions. The alias field_name is `tools`
(the top-level key); lossy detection sees the resource carrying
a `tools` extension and consults this concept. Adapter behaviour:
- codex adapter: emit natively when present.
- non-codex adapters: surface as lossy. This is the canonical
  example called out in [ADR-0003](../adr/0003-universal-kinds-with-adapter-capability-matrix.md)'s per-instance lossy addendum
  and [ADR-0010](../adr/0010-mcp-server-schema.md) — silently dropping per-tool approval is exactly
  the failure mode dotpack exists to prevent. Install proceeds
  only with `--allow-lossy`. Also a security-agent input per
  [ADR-0001](../adr/0001-empirically-derived-schema-via-corpus-survey.md).

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `tools` |

