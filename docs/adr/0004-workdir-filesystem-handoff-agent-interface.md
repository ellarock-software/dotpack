# Workdir filesystem-handoff agent interface

## Context

`$DOTPACK_AGENT_CMD` ([ADR-0002](./0002-pluggable-dotpack-agent-cmd-not-anthropic-sdk.md)) drives three agents: translator, reviewer, security. The translator's output is a *directory tree of files* (the translated resource plus its associated assets), not a single text response. The reviewer/security outputs are small structured verdicts. We need one uniform interface that handles both shapes.

## Decision

dotpack creates a per-invocation workdir with `input/` (source files for the agent to read) and an empty `output/` (where the agent writes results). dotpack invokes `$DOTPACK_AGENT_CMD <workdir>` and, when the command exits, reads `output/` for the result. All three agents use the same contract; the prompts differ in what they ask the agent to write.

## Why

A stdin→stdout text contract works for reviewer/security (small JSON verdict) but forces the translator to inline-encode multiple file contents into one text blob — large prompts, brittle parsing, and fertile ground for the LLM to hallucinate file contents. Filesystem handoff matches how all the candidate LLM CLIs (Gemini, Codex, local agent wrappers) already work, gives dotpack a preserved workdir for post-mortem debugging on failure, makes parallelism trivial (each agent gets its own workdir), and unifies the interface across all three pipeline stages.

## Consequences

The configured `$DOTPACK_AGENT_CMD` **must support tool-use** (read/write files). Simple wrappers like `curl | jq` cannot serve as the agent runtime — see [ADR-0002](./0002-pluggable-dotpack-agent-cmd-not-anthropic-sdk.md). Workdirs are created under `~/.dotpack/workdirs/<run-id>/` and retained on failure; successful runs clean theirs up. Reviewer + security run in parallel against the same translator-output workdir copied into their respective `input/`s.
