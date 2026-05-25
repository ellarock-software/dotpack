# Schema and template as dotpack's anchor

## Context

The original handoff framed dotpack as a `gh skill` extension — "install/update/search agent resources from GitHub" — with multi-agent support and an `dotpack.toml` manifest as additional concerns. Three plausible value-prop framings exist: (a) multi-agent unifier, (b) declarative Brewfile-for-agent-resources, (c) `gh skill`++ with more kinds.

## Decision

dotpack's core IP is **the schema and on-disk template** for agent resources, kept minimal-but-precise. Multi-agent support, the manifest, and the extended kind set are all *consequences* of having a common schema, not the primary anchor. Adapters materialize schema-conformant resources into agent-native form; translators bridge wild resources into the schema; the install pipeline is "validate against the schema, materialize via an adapter."

## Why

The schema-as-anchor framing positions dotpack as a *standard-setter* rather than a *converter*, which is the only framing that scales as new agent hosts and resource kinds emerge. Multi-agent-unifier (a) and gh-skill++ (c) both make dotpack a wrapper around third-party formats; both bind dotpack's value to ecosystem stability outside its control. The schema-first framing also makes the per-kind survey ([ADR-0003](./0003-empirically-derived-schema-via-corpus-survey.md)) the load-bearing first task — which it should be.

## Consequences

Every other architectural decision falls out of this: the validator/translator split, the LLM trust gate, the workdir interface, the cache, per-host kind support and lossy detection, the manifest. If dotpack ever stops investing in the schema, the rest of the architecture loses its center.
