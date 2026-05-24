# Local cache + opt-in upstream PR for translated resources

## Context

A translation that passes the reviewer + security gates is expensive to produce (one translator LLM call + two parallel review calls). The translated artifact needs to live somewhere so subsequent installs of the same (source, version) skip the full pipeline. Options span a spectrum from "private local cache" to "central dotpack-run registry."

## Decision

Translated artifacts persist to `~/.dotpack/cache/<owner>/<repo>/<sha>/<path>/`. Future installs of the same (source, version) install from cache and skip the full pipeline. A separate `dotpack contribute <source>` command optionally opens a PR back to the source repo with the translated copy + dotpack metadata, letting users push their translations upstream when they want to.

## Why

A local cache gives zero-friction default behavior — every user pays the translation cost once per (source, version) they install, and amortizes it across all subsequent uses. Opt-in upstream contribution gives a path to share without forcing it. A central dotpack-managed registry would have lower aggregate translation cost but turns dotpack into a service — hosting, moderation, governance — which is a much bigger commitment than the project is ready for. Mandatory PR-back creates blocking friction on private/abandoned/personal repos.

## Consequences

Users have no shared cache by default — early adopters pay translation cost in parallel. If `dotpack contribute` adoption is high enough, source repos become canonical homes for dotpack-conformant translations and downstream users stop translating entirely (best case). If adoption is low, the cache stays private and translation cost stays constant per user. Revisit (probably with a central registry) if the per-user cost becomes a real complaint.
