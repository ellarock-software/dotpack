# Changelog

All notable changes to dotpack are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
from the first tagged release onward.

## [Unreleased]

### Removed

- The bundled Sponsio post-install lifecycle task, its example configuration
  (`sponsio.yaml`), and its documentation. `--run-lifecycle` remains as an
  extension point, but the public distribution now ships no bundled external
  lifecycle task and stays external-tool-neutral.

### Fixed

- Reinstalling the same resource into the same target now removes files owned
  by the prior record that are absent from the replacement source.
- `uninstall` can select one of several same-ID project installs from the
  current project root or an explicit `--target`.

## [0.1.0] - 2026-07-07

First public release.

### Added

- Portable `.agents` schema and CLI covering the `skill`, `agent`, `rule`,
  `command`, `memory`, `mcp-server`, and `hook` resource kinds.
- Host adapters: `claude-code`, `gemini-cli`, `antigravity-cli`, `codex`,
  `opencode`, `hermes`, and the `agents-cli` umbrella target. Per-operation
  support is intentionally partial where a host lacks a native concept.
- Commands: `install`, `install-all`, `import`, `list`, `uninstall`,
  `inventory`, `sync-back`, `reset-materialized`, `reconcile`, `prune`,
  `scan-skills`, `baseline-skills`, and `version`.
- Flexible source layouts (`--kind-path`, per-kind path aliases) and GitHub
  sources (`github:OWNER/REPO`, `github:OWNER/REPO@REF`, and HTTPS URLs).
- Manifest-based install provenance (`~/.dotpack/installs.yaml`) with
  reconcile/prune drift handling; dotpack removes only what it can prove it
  wrote.
- Native SkillSpector gating that scans skill packages before any
  skill-bearing workflow reads or materializes them.
- Optional, opt-in post-install lifecycle verification via `--run-lifecycle`.
- Apache-2.0 license, `NOTICE`, security policy, and contributor documentation
  including an adapter-authoring guide.
- CI: gofmt, `go vet`, tests, race detector, golangci-lint, go-licenses,
  govulncheck, a cross-platform build matrix, gitleaks secret scanning, and a
  strict mkdocs docs build.

[Unreleased]: https://github.com/ellarock-software/dotpack/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ellarock-software/dotpack/releases/tag/v0.1.0
