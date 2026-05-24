# Universal kinds + adapter capability matrix; default-deny lossy installs

## Context

Different agent hosts have different first-class concepts. A Claude Code "skill" (frontmatter + body + scripts) is not structurally identical to a Cursor "rule" (markdown injected into context) or a Codex AGENTS.md file. dotpack has to decide whether `kind` means the same thing across hosts (universal taxonomy) or is host-specific (`claude-code:skill` ≠ `cursor:rule`).

## Decision

Kinds are **universal**: dotpack defines one taxonomy (`skill`, `agent`, `command`, `memory`, `hook`, `mcp-server`). Each adapter publishes a **capability matrix** declaring per-kind support as `native` (first-class in the target), `lossy` (maps to a related concept with fidelity loss), or `unsupported`. Default install policy **refuses lossy installs** unless the user passes `--allow-lossy`.

## Why

Universal kinds keep dotpack's schema and adapter contract small; otherwise every cross-host install would require explicit kind translation and the schema explodes per ecosystem. The capability matrix is honest about ecosystem mismatch — a future reader sees exactly what each adapter can and can't do, and lossy mappings can't sneak in silently and break a user's expectation that "install acme/code-review --agent X" produces equivalent behavior across `X`s. Default-deny on lossy preserves the multi-host value prop: if `--agent agents-cli` claims to install your skill, by default that means a real install, not a degraded approximation.

## Consequences

The capability matrix is a load-bearing per-adapter artifact and must be kept current as ecosystems evolve. Adapters that mark a kind `unsupported` produce a clear error rather than a silent partial install. Users who *want* lossy behavior have an explicit flag — informed consent. The matrix data structure should be queryable: `dotpack adapter list` and `dotpack adapter caps <name>` are natural followups.

**Per-instance lossy detection (added 2026-05-24, after ADR-0014 update).** The three-level matrix (`native` / `lossy` / `unsupported`) is per-(kind, adapter) — it doesn't express "claude-code is lossy *only when* the source resource uses fields the Claude adapter can't honor." With Codex MCP-server added to the corpus, that gap matters: a Codex resource carrying `default_tools_approval_mode`, `enabled_tools`, or `tools.<id>.approval_mode` is losslessly installable on Codex but lossy on Claude (the fields are dropped). MVP needs adapter-side `lossy_when_fields: [...]` declarations and a per-resource check at install time — when any listed field is present in the source, treat the install as lossy and require `--allow-lossy`. Deferred to task #5 (agents-cli design) / task #7 (MVP). Until then, the matrix expresses the cross-host-common-core lossy/native rating; per-instance promotion to lossy is the implementation step.
