# Pluggable `$DOTPACK_AGENT_CMD` runtime, not an Anthropic SDK

## Context

dotpack's translator/reviewer/security pipeline needs an LLM runtime. The default-obvious choices in early 2026 are Anthropic-branded products: Claude Code headless mode (`claude -p`), Claude Agent SDK (Python or TS), or the raw Anthropic SDK from Go. dotpack itself is a Go CLI; integrating any of these would couple distribution and authentication to one vendor.

## Decision

The LLM runtime is **a user-configured shell command** referenced as `$DOTPACK_AGENT_CMD` (e.g., `gemini -p`, `codex -p`, a local Ollama wrapper, or a custom script). dotpack invokes it with a workdir-based interface (input files in `input/`, expected output files in `output/`) and never embeds an Anthropic SDK or invokes the `claude` CLI directly.

## Why

Both Claude Code headless mode and Claude Agent SDK are metered separately from the user's Claude Code subscription, making per-install agent pipelines surprisingly expensive at any non-trivial scale. The user has flagged this as a hard rule on multiple prior sessions. Delegating the LLM runtime to a user-chosen command lets the user control billing through whichever LLM service they're already paying for (their OpenAI account via `codex -p`, their Google quota via `gemini -p`, a local model with zero per-call cost, etc.), keeps dotpack a thin Go binary with no SDK dependencies, and future-proofs against new headless LLM CLIs.

## Consequences

The configured CLI must support tool-use (file-write) because the translator's output is a directory tree of files, not a single text response — see [ADR-0004 workdir handoff](./0004-workdir-filesystem-handoff-agent-interface.md). Simple wrappers like `curl | jq` won't work as `$DOTPACK_AGENT_CMD`. dotpack ships prompts that must work across the configured CLI's quirks; integration tests use a mocked fake CLI, with real-CLI tests gated behind an env flag.
