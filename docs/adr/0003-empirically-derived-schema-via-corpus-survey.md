# Empirically-derived schema via curated corpus + LLM-assisted survey

## Context

dotpack's [schema](../../CONTEXT.md) (per kind) is the contract that the validator gates on, the translator targets, the reviewer checks fidelity against, and adapters consume. It must be precise enough to be LLM-translatable and minimal enough that adapters and authors don't drown in fields.

## Decision

The schema is **derived empirically**, not designed top-down. For each MVP kind (skill, agent, command, memory, hook, mcp-server), we curate a `schema-corpus.yaml` of 5-10 well-respected real-world source repos and run a one-shot LLM-assisted survey (`scripts/survey.sh`) that fetches each example's frontmatter (or config fragment, for hook/mcp-server), clusters fields, and proposes a per-kind schema. Humans review the proposal before committing to `schema/{kind}.yaml`.

## Why

Top-down schema design risks coining names that don't match what real authors actually use ("trigger" vs "event" vs "on" for hooks, etc.), and would force the translator to map between dotpack-coined fields and the real ecosystem instead of standardizing what's already there. Empirical grounding produces a defensible "we picked X because corpus did Y" rationale, surfaces ecosystem disagreements as data, and clarifies which fields are universal vs ecosystem-specific. Anthropic skills, awesome-claude-code, Gemini extensions, and Codex skills all exist and have stable enough conventions to survey.

## Consequences

Schema survey is a **Phase 0 task** that blocks all Go code: validator, translator prompts, reviewer rubric, and adapter materialization all consume the schema. Schemas may need re-derivation as ecosystems evolve; the corpus file gives us a reproducible baseline for re-runs. The survey script doubles as the implementation of the future `dotpack survey {kind}` subcommand.

**Schema-as-contract (added by [ADR-0016](./0016-agents-cli-adapter-fan-out-and-schema-driven-lossy-detection.md)).** This ADR originally framed the schema as the *validator's* input; ADR-0016 expanded the role. The schema is now the canonical registry for adapter behaviour as well: per-host source locations, key-name aliases, event-name aliases, transport discrimination, canonical concepts and per-host field-name aliases for `deliberately_excluded` extensions, and the inline reasons that drive per-instance lossy detection. Adapters are mechanical consumers; they consult the schema rather than restate it. Consequence for the survey methodology: every schema entry that an adapter or future contributor (human or AI) might consume must carry inline documentation sufficient to extend it without out-of-band knowledge — see ADR-0016 §10 for the required content (semantics, host support, adapter behaviour, stage ownership of `ecosystem_notes` items). The survey prompt should produce this documentation as part of the schema proposal; the ADR author refines it during the commit pass.
