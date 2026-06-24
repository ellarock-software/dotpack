# Open adapter, umbrella, and merge-backend registries

## Context

dotpack's product intent is universal coverage of LLM coding tools across every
operation. The implementation, however, treated the *current* host matrix as the
architectural boundary:

- Per-host adapters were hard-coded in `internal/cli/install.go` via the
  `adapterFactories` map, and umbrellas via `umbrellaFactories`. Adding a host
  meant editing CLI core. `internal/cli/sync.go` (`scanMaterializedFiles`) and
  `internal/cli/root.go` re-hard-coded the same host→layout table.
- The only umbrella, `agents-cli`, declared `command` and `memory` *unsupported*
  even though every sub-adapter already supported both.
- Config-fragment merging dispatched by a closed `mergedFormat` enum + switch
  wired only for `.json` and `.toml`.
- README presented a closed "Supported Targets" matrix; CONTRIBUTING had no
  documented path to add an adapter.

This ADR makes those extension points first-class so onboarding a tool or a
config format is mechanical and local, without editing scattered core
switchboards. It supersedes the `adapterFactories`/`umbrellaFactories` location
described in [ADR-0012 §10](./0012-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md);
the umbrella *semantics* in ADR-0012 are unchanged.

## Decision

### 1. Self-registration adapter + umbrella registry

A new package `internal/adapter/registry` holds the adapter factory map and the
umbrella declarations as data. It imports only `{adapter, dirs, resource}` — a
strict superset of what `internal/adapter` imports — so any adapter sub-package
can import it without a cycle, and it must never import the concrete adapter
packages.

- Each adapter sub-package registers itself from an `init()`:
  `registry.RegisterAdapter(hostID, func(d) adapter.Adapter { return New(d) })`.
  The factory closure is mandatory (Go has no return-type variance).
- `internal/adapter/all` blank-imports the concrete adapter packages (and the
  umbrella declaration) for their `init()` side effects. Commands import `all`
  once instead of naming each host.
- `internal/cli` resolves hosts/umbrellas only through the registry
  (`Build`, `IsAdapter`, `IsUmbrella`, `BuildUmbrella`, `AdapterHostIDs`), and
  calls `registry.Validate()` from its own `init()` — which runs after all
  registrations because Go runs imported packages' `init()`s first. `Validate`
  reproduces the prior fail-fast cross-reference checks (every sub/writer is a
  registered adapter; every writer is in `subs`).

Onboarding a host is now: create `internal/adapter/<host>/`, implement `Adapter`
(+ optional `LayoutDescriber`), self-register in `init()`, add one blank import
to `internal/adapter/all`, and add a `<Host>Home` field to `dirs.Dirs`. No core
switchboard edit. This is proven by a test that registers a *fake* host and
installs through the unchanged orchestrator.

### 2. `agents-cli` covers all seven operations

The `agents-cli` umbrella self-registers in `internal/adapter/all` and now lists
`command` and `memory` as fan-out writers across all sub-adapters. The writes are
distinct, non-overlapping files per host (`.gemini/commands/x.toml` vs
`.antigravity/commands/x.md` vs `.codex/commands/x.md`; `GEMINI.md` vs
`ANTIGRAVITY.md` vs `AGENTS.md`), so the existing aggregate-plan / preflight
machinery handles them unchanged. Skill remains write-once to the shared
`~/.agents/skills/` convergence path.

### 3. Pluggable config-merge backends

`internal/orchestrator/mergedkeys.go` no longer dispatches on a `mergedFormat`
enum. A `mergeBackend` interface (`apply`, `unmerge`, `readRootForPreflight`,
`parsePath`) is keyed by file extension in a registry (`mergebackends.go`).
`.json` and `.toml` wrap the existing functions byte-for-byte; a real `.yaml` /
`.yml` backend (gopkg.in/yaml.v3) ships to prove the set is open. The
format-agnostic map-walk primitives and the `json.Marshal`-based `selectorFor`
content hash stay shared across all backends, so append-uninstall identity is
format-independent. A new format is one `mergeBackend` + one `registerBackend`.

### 4. Per-operation support is per-adapter data

Support for an operation is expressed by an adapter's policy (`filedrop` Layouts
membership / `configfrag` Kinds membership), surfacing the standard
`kind X not yet supported` error when absent. The new **OpenCode** adapter
(`internal/adapter/opencode`) ships a deliberately *partial* matrix — skill,
agent, command, memory, mcp-server (JSON into `opencode.json` `$.mcp`), with rule
and hook unsupported — and needed no special-casing anywhere in core.

### 5. Registry-driven scan and help

`scanMaterializedFiles` and `root.go`'s help iterate `registry.AdapterHostIDs()`
and an optional `adapter.LayoutDescriber` (`DescribeLayouts() []KindLayout`)
instead of a hard-coded host→layout table, so a new host is scanned and listed
automatically.

## Consequences

- Adding a host or a config format is mechanical and local; core flow names no
  concrete host. Proven by openness tests (fake adapter, fake merge backend).
- Existing behavior for claude-code, gemini-cli, antigravity-cli, codex, and
  agents-cli is preserved (the JSON/TOML backends wrap the prior functions; the
  registry reproduces the prior validation).

### Gap register (irreducible / backlog)

- **codex `.agents/skills` write-once convergence** stays a documented umbrella
  special case (the shared read path for codex + gemini).
- **Per-host hook event-name remaps** (`PreToolUse`/`PostToolUse` ↔
  `BeforeTool`/`AfterTool`) live in each adapter's emit functions.
- **OpenCode extension fidelity**: opencode is not yet in `schema/*.yaml` host
  aliases, so resource *extensions* are dropped (lossy) on opencode installs;
  universal-core fields install fine. Adding the aliases is the follow-up.
- **Pi and Hermes adapters** are named backlog; the onboarding path is the one in
  §1, only per-host paths need confirming.
