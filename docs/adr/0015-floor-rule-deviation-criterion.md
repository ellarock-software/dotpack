# When to deviate from the empirical floor rule

## Context

[ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md) establishes the methodology: derive schemas from a curated corpus, exclude fields appearing in fewer than 2 examples ("the 2-example floor"). Two of the six Phase 0 ADRs deviate from the strict empirical reading:

- **ADR-0010 (`agent`)** marks `model` as **optional** despite **8/8 corpus presence**.
- **ADR-0011 (`command`)** keeps `allowed-tools` despite **1/7 corpus presence**.

The pattern matters for future re-surveys: a different corpus might re-derive these schemas, and without writing down *why* we deviated, the deviations would look like errors and get "corrected" back to the strict empirical reading.

## Decision

Floor-rule deviations are permitted in exactly two cases, both requiring explicit ADR rationale:

### Deviation 1: present in corpus, optional per spec → mark optional

When a field appears in 100% (or near-100%) of the corpus but the **published host spec explicitly makes it optional** with a useful default behaviour, the schema marks it optional.

**Why:** 100%-presence in corpus may reflect author convention rather than ecosystem requirement. Forcing the field through the schema means every translated resource picks up a field the host didn't demand, and resources from corners of the ecosystem where the convention doesn't hold get rejected by the validator. The cost of an honest "optional" mark is one false-positive (a missing field that *could* have been required); the cost of a forced "required" mark is structural over-fitting to the surveyed corpus.

**Example:** `agent.model` is 8/8 in our corpus but Gemini's spec inherits from parent session when omitted. Marking it required would force every Gemini-style agent through the translator to add a noop `model:` field.

### Deviation 2: absent or sub-floor in corpus, but **runtime-semantics-changing** per spec → keep

When a field appears below the 2-example floor (typically 0/N or 1/N) but **its presence vs absence changes runtime behaviour in a way the user has chosen** (not a default), the schema keeps it.

**Why:** The empirical floor protects against field-name-stuffing — including every documented spec field in the schema would bloat it with rarely-used noise. But some sub-floor fields are *semantically load-bearing when present* — stripping them during translation silently changes what the install does, which is worse than carrying a low-use field.

**Example:** `command.allowed-tools` is 1/7 in our corpus. But for the one command that uses it (anthropics official commit-push-pr), absence would mean every Bash invocation in the command prompts the user for approval; presence auto-approves the listed Bash filters. Stripping it during translation silently degrades UX. Keep.

## Why deviations are documented per-ADR, not handled by a smarter prompt

Both deviations require *external knowledge* the survey LLM doesn't have:

- For Deviation 1, the survey would need to know the documented host spec. We could feed it as prompt input, but per-host spec text changes faster than the survey methodology should.
- For Deviation 2, the survey would need to model the runtime consequence of field-stripping. This is host-implementation knowledge.

The methodology stays empirical at the survey stage; ADR authors apply these two patterns when committing the final schema. The survey output is one input; the ADR is the synthesis.

## Why NOT a third deviation: "Important per docs, just adopt it"

A tempting third deviation pattern is "the field is documented in the spec as important, so include it even though we didn't see it in the corpus." We reject this. If a field is documented but never appears in real-world usage, the spec is aspirational; including it weakens the schema's empirical claim ("we picked these fields because real authors actually use them"). Examples we excluded under this principle: agent's `maxTurns`, `disallowedTools`, `permissionMode`; command's `argument-hint`; mcp-server's `cwd`, `timeout`, `trust`. All documented, none in corpus, all excluded.

The contrast with Deviation 2 is sharp: Deviation 2 covers fields that **do appear in the corpus** (just sub-floor) and have a concrete runtime consequence. Pure docs-aspiration without corpus presence stays excluded.

## Consequences

**Re-surveys must apply the same rubric.** A future re-run of `scripts/survey.sh agent` on a fresh corpus will probably re-derive `model: required (8/8)`. The deviation pattern then triggers the ADR author to check whether Gemini's spec still makes it optional. The check is human-applied, not script-applied.

**Floor-rule strictness varies by kind, but the criterion is consistent.** Reading the ADRs in order, the schemas tighten or loosen on optional fields case by case. ADR-0015 makes the criterion uniform: corpus-100% + spec-optional → optional; sub-floor + semantically-load-bearing → kept. Anything else stays at the strict floor.

**Survey methodology refinements continue separately.** ADRs 0009, 0010, 0011, 0014 each surfaced and fixed a different prompt weakness (grep-verify, concrete-failure-mode, 100%-or-optional, per-entry-counting). Those land in `scripts/survey.sh`. ADR-0015 captures a layer above the prompt — the ADR-author's checklist when committing schemas.

## Artefacts

- ADR-0010, ADR-0011 — the two existing deviations this ADR retroactively justifies.
- ADR-0003 — the floor-rule methodology this ADR amends.
- `scripts/survey.sh` — prompt-level refinements (separate from this criterion).
