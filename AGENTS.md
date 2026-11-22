# AGENTS.md - dotpack

Cross-repo corporate skills, hooks, and guidance live in sibling `../ellarock-home/AGENTS.md` (`git clone https://github.com/ellarock-software/ellarock-home.git` if missing).

`dotpack` is a Go CLI that validates canonical `.agents` resources and translates them into host-native files for Claude Code, Codex CLI, Gemini CLI, Antigravity CLI, OpenCode, and `agents-cli`.

## Core Behavior

- `dotpack import`: ingests native host configuration into canonical `.agents` resources.
- `dotpack install` / `install-all`: materializes canonical resources cleanly into target projects.
- `dotpack inventory` / `sync-back` / `reset-materialized`: reconciles canonical resources and materialized output.

## Working Rules

- Prefer public, generic examples; never commit secrets, local absolute paths, generated host output, or organization-specific `.agents` packages.
- Preserve filesystem-first behavior: adapters produce plans, orchestrator applies plans, and manifests define ownership.
- Keep lifecycle hooks optional and external-tool-neutral. Never restore retired Sponsio references or dependencies.
- Update CLI help, README, docs, and tests together when behavior changes.

## Validation

Run Go tests, docs build, and secret scanning per `CONTRIBUTING.md`:

```bash
gofmt -l $(git ls-files '*.go')
go vet ./...
go test ./...
go build ./cmd/dotpack
```

`govulncheck` may send dependency metadata to `vuln.go.dev`. Do not submit by default; run `scripts/run-govulncheck.sh --allow-vuln-db-submit` only after explicit approval.
