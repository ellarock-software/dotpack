# SkillSpector Security Bypass Design

**Date:** 2026-07-25
**Status:** Approved for implementation

## Objective

Add an explicit, auditable way to omit named skills from dotpack's
SkillSpector security scan. The option is intentionally named
`--skill-bypass-security` so callers cannot mistake it for ordinary discovery
or selection filtering.

## Command Surface

The following commands accept a repeatable string-array flag:

```text
--skill-bypass-security <name>
```

- `dotpack install`
- `dotpack install-all`
- `dotpack inventory`
- `dotpack import`
- `dotpack sync-back`
- `dotpack scan-skills`

Multiple skills require repeating the flag:

```sh
dotpack install-all \
  --from .agents \
  --skill-bypass-security legacy-a \
  --skill-bypass-security legacy-b
```

`baseline-skills` does not accept the flag because it authors reviewed finding
suppressions rather than enforcing the security gate.

## Central Policy

All commands use one central filter operating on `skillScanSelection`. The
filter receives the already-selected scan targets and the requested bypass
names, then returns:

1. the remaining targets that SkillSpector must scan; and
2. the targets explicitly bypassed by the caller.

The filter applies these rules:

- Match exact resolved skill names. A skill's parsed frontmatter `name` is
  authoritative; the containing directory name remains the existing fallback
  when parsing does not produce a name.
- Treat repeated identical bypass arguments as one request.
- Fail closed when any requested name does not exist in the current selection.
  This includes names removed by `scan-skills --skill` or `--changed`; a bypass
  must have an observable effect in that invocation.
- Sort reported names for deterministic output.
- Do not mutate baseline handling. A baseline suppresses reviewed findings
  after a scan; a security bypass prevents a scan from running for that skill.

For `install`, using the flag with a non-skill resource is an error. For
commands whose invocation contains no selected skills, providing any bypass
name is also an error.

## Command Flow

Each command follows the same ordering:

1. Discover or resolve skill targets.
2. Apply ordinary positive selection such as `scan-skills --skill` and
   `--changed`.
3. Apply the central security-bypass filter.
4. Report the bypassed targets.
5. Provision and invoke SkillSpector only when targets remain.
6. Continue the command's normal operation if the remaining scan targets pass.

`sync-back` currently scans each materialized skill separately. It will first
build one selection from all sync-back skill primary files, then apply the
central filter once. This preserves fail-closed validation across the entire
invocation and avoids incorrectly rejecting a valid bypass merely because the
first scanned file has a different name.

## Audit Output

Every accepted bypass is visible in human-readable command output:

```text
SECURITY BYPASS: SkillSpector skipped skill "legacy-skill"
```

Structured SkillSpector aggregate output gains a
`security_bypassed_skills` array containing each bypassed skill's resolved name
and relative path. Existing result and summary fields remain compatible.

When all selected skills are bypassed:

- SkillSpector runtime provisioning and execution are skipped.
- The command still emits the human-readable warning.
- Commands that normally produce an aggregate scan artifact write a
  zero-scanned aggregate containing the bypass records.
- The enclosing operation continues because the caller explicitly bypassed
  every selected skill.

This is invocation-local authorization. Bypasses are not persisted in config,
manifests, baselines, or future invocations.

## Error Handling

Security-sensitive validation errors are terminal and occur before
materialization or import:

- `skill security bypass name(s) not selected: <names>`
- `--skill-bypass-security is only valid when installing a skill`

No partial bypass is applied when one requested name is invalid. No resource is
written before bypass validation and the remaining mandatory scans complete.

## Compatibility

Invocations that omit `--skill-bypass-security` retain their existing behavior.
The existing automatic-baseline lookup and partial-baseline changes already in
the working tree remain intact. JSON consumers only receive a new additive
field.

## Testing

Automated coverage must prove:

- the central filter removes exact matches, deduplicates repeats, sorts output,
  and rejects unknown or ineffective names;
- `scan-skills` scans remaining targets and reports bypassed targets;
- all-bypassed direct scans avoid SkillSpector runtime provisioning and still
  produce auditable output;
- `install` rejects the flag for non-skill resources;
- `install`, `install-all`, `inventory`, `import`, and `sync-back` pass the
  requested names through the same central policy;
- automatic gates scan all non-bypassed skills and block on their findings;
- an invalid bypass prevents downstream writes; and
- existing behavior remains unchanged when the flag is absent.

Repository-wide validation follows `CONTRIBUTING.md`: formatting, module
tidiness, vet, all Go tests, binary build, strict documentation build, and
secret scanning. The Ella Rock learning-system validator runs before any
completeness claim.
