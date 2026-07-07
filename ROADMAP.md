# Roadmap

dotpack's north star is **universal coverage of LLM coding tools for every
resource operation** (`skill`, `agent`, `rule`, `command`, `memory`,
`mcp-server`, `hook`). The shipped [support matrix](README.md#current-host-coverage)
is deliberately partial: a blank cell means a host has no native concept for
that operation today, not that dotpack refuses to grow into it.

This roadmap is directional, not a commitment to dates or ordering. The best
signal for what lands next is open issues — especially
[adapter requests](https://github.com/ellarock-software/dotpack/issues/new?template=adapter_request.yml).

## Near-term

- **More host adapters.** The adapter path is intentionally self-contained
  (see [ADR-0014](docs/adr/0014-open-adapter-and-merge-backend-registries.md)
  and the "Adding a New Host Adapter" guide in
  [CONTRIBUTING.md](CONTRIBUTING.md)). Community adapter contributions are
  welcome.
- **Fill in partial matrix cells** for existing hosts as those tools gain
  native surfaces for kinds they don't support yet.
- **Broader `import` support.** `import` currently ingests Claude Code trees;
  extend it to more hosts.

## Medium-term

- **More config-merge backends** beyond JSON/TOML/YAML as new hosts need them.
- **Richer reconciliation** and reporting (`inventory`, `reconcile`,
  `sync-back`) for config-fragment kinds.
- **Distribution:** Homebrew tap and `scoop`/`winget` packages in addition to
  `go install`.

## Longer-term / under consideration

- Optional LLM-assisted translation for non-conformant in-the-wild resources
  (the translator/reviewer/security-agent pipeline described in the archived
  design docs).
- Supply-chain hardening for releases (artifact signing, provenance).

## Non-goals

- Being a runtime. dotpack materializes configuration and records provenance;
  it does not run agents.
- Coupling to any single vendor's SDK. Hosts are data-driven adapters.
