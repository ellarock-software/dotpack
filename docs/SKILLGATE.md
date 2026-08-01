# Skill security gates

dotpack runs a mandatory security gate over every skill package before it reads
or materializes anything. This page is the operator's guide to it.

The design reasoning is in
[ADR-0016](./adr/0016-delta-skill-security-gate-and-gate-registry.md).

## The short version

```sh
# First install of a package: blocked, because nothing about it is reviewed yet.
dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope user

# Read the findings, then record the reviewed state.
dotpack approve-skill .agents --skill code-review --reason "Reviewed in PR #42"

# Now it installs, and keeps installing until something NEW appears.
dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope user
```

Commit the file `approve-skill` writes. It is a security decision, and the diff
is where it gets reviewed.

## Why the first install blocks

The default gate, `skillgate`, gates on **change**. It approves a package at a
reviewed state and blocks only findings that are *new* since that approval.

That model exists because the previous absolute gate ran about 5% precision on a
216-package corpus — 271 findings triaged, 14 real. Nobody reads 257 false
positives; they reach for `--skill-bypass-security`, which exempts a whole
package permanently, on exactly the large active packages most worth watching.

Gating on change makes that noise harmless. A package's constant findings are
baselined once and never fire again, so the gate can afford a noisy, high-recall
detector — and a high-recall detector is what you actually want.

The corollary is that an unapproved package is never trusted. There is no
"probably fine" state.

## What blocks and what does not

| Situation | Result |
|---|---|
| No approved baseline | **BLOCKED** — first sighting |
| A new finding at or above `HIGH` | **BLOCKED** |
| A new finding below `HIGH` | reported, installs |
| Content changed, no new finding | reported as drift, installs |
| An approved finding disappeared | not an event, installs |
| No `SKILL.md`, or it is a symlink | **BLOCKED** |
| A symlink escaping the package, or dangling | **BLOCKED** |
| Nothing could be hashed | **BLOCKED** |
| Detector missing, timed out, crashed, or emitted garbage | **BLOCKED** |

An unscannable package is not an approved package.

## Choosing a gate

```sh
dotpack install ... --skill-gate skillspector    # per invocation
export DOTPACK_SKILL_GATE=skillspector           # per shell
```

| Gate | Detector | Model |
|---|---|---|
| `skillgate` (default) | `cisco-ai-skill-scanner`, plus dotpack's own hidden-character check | delta |
| `skillspector` | NVIDIA SkillSpector | absolute |

Gate selection is **operator-controlled only**. dotpack never reads it from the
package being installed: a source that could choose its own gate could choose
the weakest one.

## Approving

```sh
dotpack approve-skill [source] (--skill <name>... | --all) [--reason <text>]
                      [--policy-root <dir>] [--json]
```

You must pass `--skill` or `--all` explicitly — approving an entire tree is
never the accidental result of a mistyped flag.

Baselines are written to
`<policy-root>/.dotpack/skillgate/baselines/<skill>.json` and record the
detector version, policy version, dotpack version, timestamp and your reason,
alongside the approved findings and the package content hash.

`approve-skill` refuses to write into dotpack-managed state under
`~/.dotpack`. Approving a fetched source there would persist an approval into a
throwaway cache that the gate would then read back as though it had been
reviewed.

### Approvals are tamper-evident, not tamper-proof

The review control is the pull-request diff. Anyone who can land a commit can
approve a finding — just as anyone who can land a commit can add the code the
finding describes. What the provenance fields buy is that an approval cannot be
*silent*.

dotpack also keeps a machine-local note of what you last installed against, and
tells you when a committed baseline has changed since then. That is advisory; it
never blocks.

## The hidden-character check

dotpack checks for zero-width characters, bidi controls, and Cyrillic or Greek
homoglyphs in `SKILL.md` itself, rather than delegating it to the detector.

That class cannot be delegated to a semantic analyser, because an invisible
codepoint is invisible in the analyser's input too. In testing, the detector with
its LLM engine enabled still missed 21 zero-width spaces in a real skill.

## Runtime directories

A skill that writes its own runtime state — `logs/`, `__pycache__/` — would
otherwise churn the content hash on every run. Such a directory can be excluded
from the **hash**, but it is still scanned, so a payload dropped into it still
blocks.

An exclusion must be **earned**. The package declares the path in its own
`.gitignore` *and* the name must appear in dotpack's allowlist (`logs`,
`__pycache__`, `.pytest_cache`, `.ruff_cache`, `node_modules`, `.venv`, `.git`).
A package's `.gitignore` can only narrow that list, never extend it — otherwise
a package could exclude its own `SKILL.md` and a full rewrite would report as
unchanged. Matching is anchored at the package root, except for names that
genuinely nest.

## Bypassing, and why to avoid it

```sh
dotpack install ... --skill-bypass-security legacy-skill
```

This skips the gate for one named package for one invocation, and reports that
it did. It exempts the **whole** package, so a bypass that becomes habitual is
a permanent hole. If a gate blocks something you have reviewed and accepted, the
right move is `approve-skill`, which records what you accepted; a bypass records
nothing.

## The detector

`cisco-ai-skill-scanner` (Apache-2.0), pinned by version and provisioned into a
private virtual environment under `~/.dotpack/skillgate/runtime`. Its default
engines run locally: no API key, no network at scan time. The optional LLM
engine is not enabled and is deliberately not load-bearing.

First run needs network access to install it. On an offline machine the gate
fails closed; the escapes are `--skill-gate skillspector` and
`--skill-bypass-security`, both explicit.

## Where things live

| Path | What |
|---|---|
| `<repo>/.dotpack/skillgate/baselines/<skill>.json` | approvals — **commit these** |
| `~/.dotpack/skillgate/runtime/` | the pinned detector |
| `~/.dotpack/skillgate/runs/<timestamp>-<command>/` | per-run reports |
| `~/.dotpack/skillgate/seen/` | machine-local install notes |
