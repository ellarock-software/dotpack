# Native SkillSpector skill scanning

## Context

dotpack already had the right product boundary for skill hardening:

- portable canonical skill packages;
- a public CLI that can target one skill or a whole source tree; and
- an existing model where translation and lifecycle verification are separate
  concerns, even when a security gate needs to become mandatory.

What it lacked was a first-class, repo-generic scan surface. SkillSpector
existed only as an external wrapper in a separate control-plane repository,
which made the feature non-portable and tied public skill scanning to
organization-specific manifests, paths, and population rules.

## Decision

dotpack owns SkillSpector skill scanning directly.

### 1. Flat CLI verbs

dotpack adds two top-level commands:

- `scan-skills [source]`
- `baseline-skills [source]`

These match the existing verb-based CLI style (`install-all`, `sync-back`,
`reset-materialized`) and keep the feature discoverable without inventing a
nested subcommand tree.

### 2. Scan population follows dotpack source semantics

The scan commands accept:

- a skill directory containing `SKILL.md`;
- a direct `SKILL.md` path;
- a canonical `.agents` tree;
- a project root containing `.agents`; or
- a non-canonical root when the user supplies `--skills-path` or
  `--kind-path skill=...`.

This reuses dotpack's existing source-layout model instead of introducing a
separate SkillSpector-specific notion of where skills live.

### 3. Static-only by default

dotpack always invokes SkillSpector with `--no-llm` unless a future change adds
an explicit opt-in for broader modes. The public default is static-only.

### 4. Dotpack-owned runtime and pin

dotpack provisions SkillSpector under `DOTPACK_DOTPACK_HOME/skillspector`,
pins the upstream repo to commit
`ac6b41b7a28b7b3d9001e43fbbf710d4267d5a7c`, records the expected package
version `2.3.5`, and persists runtime metadata in
`DOTPACK_DOTPACK_HOME/skillspector/runtime.json`.

This keeps the runtime generic, reproducible, and independent of any external
repo wrapper.

### 5. Baselines are explicit per-skill policy

`baseline-skills` writes one YAML file per skill at
`<baseline-dir>/<skill-name>.yaml`.

`scan-skills --baseline-dir ...` applies those baselines and fails closed if a
selected skill is missing its baseline file. This keeps baseline policy
reviewable as ordinary files rather than hidden state.

### 6. Gating is mandatory on skill-bearing workflows

`scan-skills` gates by default: unsuppressed findings return non-zero unless
the user passes `--report-only`.

Skill-bearing workflows invoke the same static SkillSpector gate automatically
before dotpack reads or materializes skill content. This includes:

- `install` when the selected resource is a skill;
- `install-all` when the discovered source layout contains skills;
- canonical `inventory` comparisons;
- Claude Code `import`; and
- `sync-back` when it is about to copy skill output back into canonical
  `.agents`.

Automatic gates look for reviewed baselines under
`<policy-root>/.dotpack/skillspector/baselines`, with `policy-root` resolved to
the enclosing project directory for canonical `.agents` and host-native roots
such as `.claude`.

## Consequences

- dotpack now exposes public, generic skill scanning without any dependency on
  organization-specific manifests or wrapper repositories.
- Skill hardening now applies at the runtime seam where dotpack actually reads
  or transforms skill packages, which makes the security gate mandatory instead
  of documentary.
- The runtime pin and metadata make scan behavior reproducible across machines.
- Baseline policy is durable and reviewable, and automatic gates can pick up a
  repository-local baseline directory without additional flags.
