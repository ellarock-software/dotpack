# AGENTS.md - dotpack

## Repository Context

dotpack is a Go CLI that validates portable `.agents` resources and translates
them into host-native agent configuration files.

Use the repository's public documentation as the source of truth:

- `README.md` for user-facing behavior and commands.
- `CONTRIBUTING.md` for development workflow and validation commands.
- `docs/` for schema and architecture documentation.
- `GEMINI.md` for concise agent-facing project context.

## Working Rules

- Prefer public, generic examples over organization-specific examples.
- Do not commit secrets, local absolute paths, generated host output, or private
  `.agents` packages.
- Preserve dotpack's filesystem-first behavior: adapters produce plans,
  orchestrator code applies plans, and manifest records define ownership.
- Keep lifecycle hooks optional. Sponsio integration is recommended hardening
  for teams that use it, not a default requirement for public users.

## Ella Rock Learning System

Before material planning, implementation, recovery, architecture claims,
verification claims, or high-blast-radius project work in dotpack, read:

- `/Users/joehosemann/code/ellarock/ellarock-docs/ellarock/Projects/Ella-Rock-Software/Learning-System.md`
- `/Users/joehosemann/code/ellarock/ellarock-docs/ellarock/Projects/Ella-Rock-Software/Lessons/README.md`
- `/Users/joehosemann/code/ellarock/ellarock-docs/ellarock/Projects/Ella-Rock-Software/Lessons/projects.json`

Obey active lessons that apply to `all` or `dotpack`. If a user correction
produces "You're right", "You are right", "I missed that", "I should have", or
similar language after a failed prompt or command, evaluate whether the failure
needs a durable lesson before closing the turn.

Run the learning-system gate before claiming adoption, currentness,
completeness, or enforcement:

```bash
cd /Users/joehosemann/code/ellarock/ellarock-docs
node scripts/validate-learning-system.mjs --root .
```

## Validation

Before calling implementation work complete, run the broad checks from
`CONTRIBUTING.md`, including Go tests, docs build, and secret scanning.
