# Universal kinds; data-driven per-host support; default-deny lossy installs

## Context

Different agent hosts have different first-class concepts. A Claude Code "skill" (frontmatter + body + scripts) is not structurally identical to a Cursor "rule" (markdown injected into context) or a Codex AGENTS.md file. dotpack has to decide whether `kind` means the same thing across hosts (universal taxonomy) or is host-specific (`claude-code:skill` ≠ `cursor:rule`).

## Decision

Kinds are **universal**: dotpack defines one taxonomy (`skill`, `agent`, `command`, `memory`, `hook`, `mcp-server`). Per-host kind support is data on the adapter, not a queryable interface artifact — for the file-drop adapter (claude-code, gemini-cli, codex), it's `Policy.Layouts` membership: a kind present in `Layouts` is supported; a kind absent is unsupported and `Plan` returns `kind X not yet supported`. Per-instance lossiness (the third state in the original three-level rating) is **not declared by the adapter**; it is computed by the orchestrator at install time from schema aliases per [ADR-0016 §8](./0016-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md). Default install policy **refuses lossy installs** unless the user passes `--allow-lossy`.

## Why

Universal kinds keep dotpack's schema and adapter contract small; otherwise every cross-host install would require explicit kind translation and the schema explodes per ecosystem.

Per-host kind support lives as data on the adapter rather than as a separately-queryable `Capabilities()` method because there is no production consumer of the queryable form — `Plan`'s typed `kind X not yet supported` error is the only enforcement point the CLI actually traverses. A parallel `Capabilities()` declaration on the same data invited drift and added an interface method without a caller. Codex-no-agent is now expressed as `KindAgent` absent from `Policy.Layouts`, which the adapter docstring annotates as a deliberate decision (not an oversight): "codex CLI documents no native agent loading directory per developers.openai.com/codex."

Per-instance lossiness is computed from the schema (per ADR-0016 §8) rather than declared by the adapter so that adding a host doesn't require restating which fields the schema already names as host-specific. The schema is the single source of truth: each `deliberately_excluded` entry carries a `canonical_concept` slug and an `aliases: [{host, field_name}, ...]` object array; the orchestrator walks the resource's extensions, looks up canonical concepts, and flags any whose alias array doesn't name the target host.

Default-deny on lossy preserves the multi-host value prop: if `--agent agents-cli` claims to install your skill, by default that means a real install, not a degraded approximation. Users who *want* lossy behavior have an explicit flag — informed consent.

## Consequences

Per-host kind support is a load-bearing per-adapter artifact (in the file-drop adapter, the `Layouts` map on `Policy`) and must be kept current as ecosystems evolve. Adapters whose `Plan` rejects a kind produce a clear error rather than a silent partial install. Users who *want* lossy behavior have an explicit `--allow-lossy` flag — informed consent.

A future `dotpack adapter caps {name}` CLI is still natural — it would render `Layouts` membership as a per-kind table for human inspection — but does not need to traverse a separate interface method to do so.

**History.** The original 2026-05-24 version of this ADR posited a three-level per-`(kind, adapter)` capability matrix (`native` / `lossy` / `unsupported`) returned by an `Adapter.Capabilities()` method. The 2026-05-25 architecture review (cards #1 and #5) collapsed that machinery: card #1 made per-host kind support data-derived from `Policy.Layouts` membership; card #5 dropped the `Capabilities()` method, `KindCapabilityMatrix`, `CapabilityLevel`, and the `Native` / `Lossy` / `Unsupported` constants. The pre-2026-05-24 mechanism for declaring host-specific fields (originally proposed as adapter-side `lossy_when_fields: [...]` lists in this ADR) was already superseded by [ADR-0016 §8](./0016-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md)'s schema-side alias arrays. The rationale (single source of truth, safer failure mode, lower new-adapter onboarding cost) is in ADR-0016. The per-resource install-time check and the default-deny-without-`--allow-lossy` policy stand.
