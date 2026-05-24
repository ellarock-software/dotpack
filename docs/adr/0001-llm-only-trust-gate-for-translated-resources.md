# LLM-only trust gate for translated resources

## Context

dotpack's install pipeline runs a [validator](../../CONTEXT.md) on every source resource. If the source is non-conformant to dotpack's schema/template, a [translator](../../CONTEXT.md) LLM agent rewrites it into a conformant form. We need to gate the translated artifact before persisting and installing it, because installed resources are *executable* (skills can ship scripts, hooks are shell, agents drive tool use).

## Decision

The gate is two LLM agents running in parallel after the translator: a **reviewer** (correctness vs source) and a **security agent** (prompt-injection, exfiltration, hidden shell). Both must approve. There is **no human checkpoint** — not even a one-time diff-and-confirm for novel sources.

## Why

The user explicitly prioritized "less burden for the user" over the trust risk a human-in-the-loop checkpoint would mitigate. Layered LLM gating with a purpose-built security reviewer is the defense-in-depth substitute. The user rejected a human-checkpoint proposal twice during the design conversation, both times *strengthening* the LLM gating rather than adding human gates.

## Consequences

LLM-only gating reduces correctness/hallucination risk but does not eliminate adversarial-input risk — a sufficiently crafted source repo can attempt to prompt-inject the reviewer or security agent. A cheap downstream mitigation that doesn't add human burden: default-deny for kinds that ship shell (`hook`, `mcp-server`) and require an explicit `--unsafe` opt-in, OR a `--strict` flag that requires the diff to be shown. Revisit if the first real adversarial input slips through.
