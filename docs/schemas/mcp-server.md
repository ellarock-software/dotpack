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
- Hermes uses YAML instead of JSON/TOML and stores servers under `~/.hermes/config.yaml mcp_servers`. The universal core fields (`command`, `args`, `url`, `env`) retain their meanings; Hermes then extends with HTTP auth/TLS keys (`headers`, `ssl_verify`, `client_cert`, `client_key`), lifecycle/timeout flags (`enabled`, `timeout`, `connect_timeout`, `supports_parallel_tool_calls`), tool exposure policy (`tools`), and server-initiated sampling policy (`sampling`).
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
- hermes adapter: emit `headers` natively.
- other adapters: surface as lossy. HTTP authentication is
  load-bearing; silently dropping headers means the install will
  fail at runtime with confusing auth errors.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `http_headers` |
| `codex` | `headers` |
| `hermes` | `headers` |

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

### Concept: `server_overall_timeout`

Overall server timeout field shared by Gemini/Antigravity/Hermes
(no corpus presence). Distinct from Codex's per-phase timeouts
(startup_timeout_sec, tool_timeout_sec) — those are separate
canonical_concepts. Adapter behaviour:
- gemini-cli / antigravity-cli / hermes adapters: emit natively
  when present.
- other adapters: surface as lossy. Adapter ergonomics note:
  if a future ADR-0017 unifies overall-vs-phase timeouts into one
  canonical_concept, this entry merges with codex_startup_timeout
  and codex_tool_call_timeout. Deferred per [ADR-0012](../adr/0012-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md) (Scenario B).

**Aliases:**

| Host | Field Name |
| --- | --- |
| `gemini-cli` | `timeout` |
| `antigravity-cli` | `timeout` |
| `hermes` | `timeout` |

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

Lifecycle flags controlling whether a configured server is active.
Codex exposes both `enabled` and `required`; Hermes exposes
`enabled`. `enabled: false` disables a server
without removing it from config; `required: true` causes Codex
to fail startup if the server doesn't initialise (vs default
warn-and-continue). Adapter behaviour:
- codex / hermes adapters: emit the fields they natively support.
- other adapters: surface as lossy. `enabled: false` is
  particularly load-bearing — silently emitting the server as
  active on Claude/Gemini contradicts the author's explicit
  intent. Install proceeds only with `--allow-lossy`.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `enabled` |
| `codex` | `required` |
| `hermes` | `enabled` |

### Concept: `hermes_connect_timeout`

Hermes-native initial connection timeout for MCP servers. Distinct
from the overall request timeout above. Adapter behaviour:
- hermes adapter: emit natively when present.
- non-hermes adapters: surface as lossy. Dropping the connect
  timeout changes failure behaviour for slow or intermittently
  reachable remote MCP endpoints.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `hermes` | `connect_timeout` |

### Concept: `hermes_parallel_tool_calls`

Hermes-native concurrency flag allowing tools from one MCP server
to run in parallel. Adapter behaviour:
- hermes adapter: emit natively when present.
- non-hermes adapters: surface as lossy. This is an explicit
  runtime-safety/performance choice by the author.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `hermes` | `supports_parallel_tool_calls` |

### Concept: `hermes_http_auth_mode`

Hermes-native HTTP auth mode selector (for example OAuth). Adapter
behaviour:
- hermes adapter: emit natively when present.
- non-hermes adapters: surface as lossy. HTTP auth mode is
  load-bearing; silently dropping it breaks remote server access.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `hermes` | `auth` |

### Concept: `hermes_sampling_policy`

Hermes-native policy for MCP server initiated sampling requests.
Adapter behaviour:
- hermes adapter: emit natively when present.
- non-hermes adapters: surface as lossy. Sampling controls are a
  real runtime capability and safety budget, not cosmetic metadata.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `hermes` | `sampling` |

### Concept: `hermes_http_tls_controls`

Hermes-native HTTP TLS controls for MCP servers, including custom
CA bundles and mTLS client certificate/key material. Adapter
behaviour:
- hermes adapter: emit natively when present.
- non-hermes adapters: surface as lossy. TLS verification and
  client-certificate auth are load-bearing security settings.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `hermes` | `ssl_verify` |
| `hermes` | `client_cert` |
| `hermes` | `client_key` |

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

### Concept: `server_tool_policy`

Host-specific server tool policy bucket. Codex uses `tools` for
per-tool approval overrides (`tools.<id>.approval_mode`);
Hermes uses `tools` for include/exclude/resource/prompt exposure
policy. The shared top-level key is enough for adapter preserve/
drop decisions, but the nested shapes are NOT equivalent and are
not canonicalised by dotpack today. Adapter behaviour:
- codex / hermes adapters: emit native `tools` values when the
  nested shape matches the host they target; foreign-shape values
  are adapter-side errors rather than silent rewrites.
- other adapters: surface as lossy. This remains a load-bearing
  security/runtime control; silently dropping it is exactly the
  failure mode dotpack exists to prevent.

**Aliases:**

| Host | Field Name |
| --- | --- |
| `codex` | `tools` |
| `hermes` | `tools` |

