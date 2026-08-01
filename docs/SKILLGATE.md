# Skill security gates

dotpack runs a mandatory security gate over every skill package before it reads
or materializes anything. This page is the operator's guide to it.

The design reasoning is in
[ADR-0016](./adr/0016-delta-skill-security-gate-and-gate-registry.md).

## The short version

```sh
# First install of a package: blocked, because nothing about it is reviewed
# yet. The refusal lists every finding and names the remedy.
dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope user

# Having read them, record the reviewed state.
dotpack approve-skill .agents --skill code-review --reason "Reviewed in PR #42"

# Now it installs, and keeps installing until something NEW appears.
dotpack install .agents/skills/code-review/SKILL.md --agent claude-code --scope user
```

**First run downloads a detector.** The gate provisions a Python virtual
environment holding `cisco-ai-skill-scanner` -- roughly 400 MB, once per machine.
dotpack says so before it starts. You need Python 3.11+ and network access for
that one run.

Commit the file `approve-skill` writes. It is a security decision, and the diff
is where it gets reviewed.

## Why the first install blocks

The default gate, `skillgate`, gates on **change**. It approves a package at a
reviewed state and blocks only findings that are *new* since that approval.

That model exists because absolute gating on a noisy detector does not survive
contact with reviewers. Nobody reads hundreds of false positives; they reach for
`--skill-bypass-security`, which exempts a whole package permanently, on exactly
the large active packages most worth watching. (The measurement behind this is in
ADR-0016; it was taken on a private corpus.)

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
| No `SKILL.md` | **BLOCKED** |
| Any symlink escaping the package, dangling, or unreadable — including a symlinked `SKILL.md` | **BLOCKED** |
| A symlink resolving inside the package to a file that exists | allowed |
| Nothing could be hashed | **BLOCKED** |
| Detector missing, timed out, crashed, or emitted garbage | **BLOCKED** |

An unscannable package is not an approved package.

## Skills fetched from a remote source

A source dotpack fetched cannot supply its own approvals -- otherwise a remote
repository would approve itself. So `github:` sources block until you approve
them into a repository *you* control:

```sh
dotpack approve-skill github:OWNER/REPO --all --skill-policy-root .
dotpack install-all --from github:OWNER/REPO --skills-path skills --skill-policy-root .
```

`--skill-policy-root` names the repository that owns approvals for the run. Like
gate selection, it is operator-controlled.

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
                      [--skill-policy-root <dir>] [--accept-at-risk-findings] [--json]
```

You must pass `--skill` or `--all` explicitly — approving an entire tree is
never the accidental result of a mistyped flag.

Approving **refuses by default** when it would record a finding at or above the
severity floor, and prints those findings instead. Pass
`--accept-at-risk-findings` to record them deliberately. Without that guard, the
natural reaction to a block — re-run with `--all` — would quietly launder exactly
the findings the gate exists to stop.

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
prints a notice when a committed baseline has changed since then. That is
advisory; it never blocks, and it only fires on machines that have installed the
package before.

## The hidden-character check

dotpack checks for zero-width characters, bidi controls, and Cyrillic or Greek
homoglyphs in `SKILL.md` itself, rather than delegating it to the detector.

That class cannot be delegated to a semantic analyser, because an invisible
codepoint is invisible in the analyser's input too. In testing, the detector with
its LLM engine enabled still missed 21 zero-width spaces in a real skill.

The scan covers **every file the package would install**, decoded as UTF-8 —
not an extension allowlist. install copies support files verbatim with no
filtering, so any file the scan skipped would be a place to hide instructions an
agent would still read.

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

First run needs Python 3.11+ and network access to install it, and takes roughly
400 MB.

On an offline machine the gate fails closed. Note that `--skill-gate
skillspector` is **not** an offline escape: that gate provisions its own Python
environment from a git clone, so it needs Python and network too. The only
offline escape is `--skill-bypass-security <name>`, which is the permanent hole
described above — so on an air-gapped machine, provision the detector once while
you still have a network.

## Where things live

| Path | What |
|---|---|
| `<repo>/.dotpack/skillgate/baselines/<skill>.json` | approvals — **commit these** |
| `<repo>` | resolved from the source, or named with `--skill-policy-root` |
| `~/.dotpack/skillgate/runtime/` | the pinned detector |
| `~/.dotpack/skillgate/runs/<timestamp>-<command>/` | per-run reports |
| `~/.dotpack/skillgate/seen/` | machine-local install notes |
