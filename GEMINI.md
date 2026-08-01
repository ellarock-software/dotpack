# Agent Context for dotpack

dotpack is a universal translator for AI-agent configuration resources. Its
canonical source format is `.agents`; its output is host-native files for tools
such as Claude Code, Gemini CLI, Antigravity CLI, Codex CLI, and the
`agents-cli` umbrella target.

## Core Behavior

- `dotpack import` ingests native host configuration into canonical `.agents`
  resources. The current importer supports Claude Code input.
- `dotpack install` materializes one canonical resource into one host or
  umbrella target.
- `dotpack install-all` materializes supported resources from a canonical
  `.agents` tree into a target project.
- `dotpack inventory`, `sync-back`, and `reset-materialized` support
  reconciliation between canonical resources and materialized output.

## Lifecycle Policy

Post-install lifecycle hooks are optional. Public examples and tests must not
assume an external lifecycle tool is installed. Ella Rock retired Sponsio on
2026-07-30, so it must not be restored as a bundled task or dependency.

## Development Expectations

- Keep examples public and generic.
- Do not commit private paths, credentials, generated host output, or
  organization-specific `.agents` trees.
- When behavior changes, update CLI help, README, docs, and tests together.
