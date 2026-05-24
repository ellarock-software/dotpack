#!/usr/bin/env bash
# scripts/runtime/gemini.sh
#
# Thin wrapper that binds the Gemini CLI to dotpack's workdir-handoff contract
# (ADR-0004). dotpack invokes `$DOTPACK_AGENT_CMD <workdir>`; the workdir holds
# `prompt.md` + `input/` and expects results under `output/`.
#
# Usage:
#   export DOTPACK_AGENT_CMD="$(pwd)/scripts/runtime/gemini.sh"
#   ./scripts/survey.sh skill
#
# Why a wrapper: `gemini -p "<prompt>"` reads a prompt string, not a workdir.
# Gemini also needs --yolo (auto-approve tools) for the agent to write files
# under output/ without prompting. cd-ing into the workdir scopes Gemini's
# default file-tool root to the survey workdir.

set -euo pipefail

workdir="${1:?usage: $0 <workdir>}"

if [[ ! -d "$workdir" ]]; then
  echo "error: workdir not found: $workdir" >&2
  exit 2
fi
if [[ ! -f "$workdir/prompt.md" ]]; then
  echo "error: missing $workdir/prompt.md" >&2
  exit 2
fi

cd "$workdir"

# --yolo auto-approves tool calls (file write needed for output/schema.yaml).
# --skip-trust marks the workdir as trusted for this session, required in
# headless/automated runs because Gemini's default-deny trust check blocks --yolo
# in unrecognised directories. The workdir is dotpack-owned, so this is safe.
gemini --yolo --skip-trust -p "$(cat prompt.md)"
