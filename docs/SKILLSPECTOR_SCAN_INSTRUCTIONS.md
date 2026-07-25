# SkillSpector Skill Gating

dotpack runs static SkillSpector scans against portable skill packages before
skill-bearing workflows read or materialize them into host-native files.

## Commands

Generate per-skill baselines:

```sh
CATALOG=/path/to/project/.agents
BASELINES=/path/to/project/.dotpack/skillspector/baselines

dotpack baseline-skills "$CATALOG" --baseline-dir "$BASELINES"
```

Run a gating scan against a canonical `.agents` tree:

```sh
dotpack scan-skills "$CATALOG" --baseline-dir "$BASELINES"
```

Scan a single skill directory or direct `SKILL.md` file:

```sh
dotpack scan-skills /path/to/project/.agents/skills/reviewer
dotpack scan-skills /path/to/project/.agents/skills/reviewer/SKILL.md
```

Scan a non-canonical skill root:

```sh
dotpack scan-skills /path/to/catalog --skills-path skills
dotpack scan-skills /path/to/catalog --kind-path skill=skills
```

Write SARIF:

```sh
dotpack scan-skills "$CATALOG" --format sarif --output /tmp/skills.sarif
```

## Automatic Gate

- Skill-bearing workflows automatically run a static SkillSpector gate before
  dotpack reads or materializes skill content. This covers `install`,
  `install-all`, canonical `inventory` comparisons, Claude Code `import`, and
  `sync-back` when skills are involved.
- Automatic gates look for baseline files at
  `<policy-root>/.dotpack/skillspector/baselines`. For canonical `.agents` or
  host-native roots such as `.claude`, `policy-root` is the parent project
  directory.
- `install`, `install-all`, `inventory`, `import`, and `sync-back` accept the
  repeatable `--skill-bypass-security <name>` flag. Exact selected skill names
  are omitted from the automatic scan for that invocation.

## Direct Commands

- `scan-skills` is static-only by default. It passes `--no-llm` to
  SkillSpector.
- `scan-skills` gates by default. If unsuppressed findings remain, the command
  exits non-zero.
- Pass `--report-only` when you want the findings in the output without a
  non-zero exit.
- Pass repeatable `--skill-bypass-security <name>` arguments only when a named
  selected skill must not be scanned. Unknown or unselected names fail closed.
- When `--baseline-dir` is set, dotpack expects a file named
  `<skill-name>.yaml` for every selected skill and fails closed if one is
  missing.

## Security Bypasses

Security bypasses are explicit, invocation-local exceptions:

```sh
dotpack scan-skills "$CATALOG" \
  --skill-bypass-security legacy-a \
  --skill-bypass-security legacy-b
```

Every accepted bypass prints a `SECURITY BYPASS` warning. JSON aggregates
record the bypassed names and paths under `security_bypassed_skills`; Markdown
and SARIF exports carry the same audit information. If other skills remain
selected, dotpack scans them normally. If every selected skill is bypassed,
dotpack skips SkillSpector runtime provisioning but still writes the aggregate
audit artifact.

Prefer reviewed baselines whenever scanning remains possible. A baseline
suppresses specific known findings after SkillSpector scans a skill;
`--skill-bypass-security` prevents the named skill from being scanned at all.

## Runtime

dotpack manages the SkillSpector runtime under `DOTPACK_DOTPACK_HOME`:

- runtime root: `DOTPACK_DOTPACK_HOME/skillspector`
- virtual environment: `DOTPACK_DOTPACK_HOME/skillspector/runtime`
- runtime metadata: `DOTPACK_DOTPACK_HOME/skillspector/runtime.json`
- run artifacts: `DOTPACK_DOTPACK_HOME/skillspector/runs/`

Pinned upstream:

- repo: `https://github.com/NVIDIA/SkillSpector.git`
- commit: `ac6b41b7a28b7b3d9001e43fbbf710d4267d5a7c`
- version at that commit: `2.3.5`

Use the scan commands when you want to inspect or export the same findings
directly, and `baseline-skills` when you need to author reviewed suppressions
for the automatic gate.
