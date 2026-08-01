# Delta skill security gate, selected through an open gate registry

## Context

[ADR-0015](./0015-native-skillspector-skill-scanning.md) made SkillSpector
scanning native and mandatory: every skill-bearing workflow scans before
dotpack reads or materializes anything, and any unsuppressed finding fails the
command. That gate is *absolute* — it compares each run's findings against
zero, and a reviewed baseline file is the only way to accept one.

Absolute gating needs a precise detector. Measured on a 216-package corpus, the
incumbent gate ran roughly **5% precision**: 271 findings were triaged by hand
against the cited file and line, and 14 were real. Twelve rule IDs produced no
real finding at all across the whole corpus.

The consequence was not that people read 257 false positives. It is that they
stopped: the practical response to that much noise is
`--skill-bypass-security <name>`, which exempts a whole package permanently.
The packages generating the most noise are the largest and most active ones —
precisely the ones most worth watching — so the gate ended up scanning
everything except what mattered. **A permanent bypass is strictly worse than a
noisy gate.**

Improving precision is not available to us; the detector is upstream. But
precision only matters because the comparison is against zero.

## Decision

### 1. Gate on delta, not on absolutes

A package is approved at a reviewed state, and only findings that are **new**
relative to that approval block. A constant noise floor is baselined once and
never fires again.

This inverts what the detector has to be good at. Precision stops mattering —
false positives are absorbed into the baseline on first review, at a cost of one
review each, ever. Recall starts mattering, because a detector that never
reports a class of problem will never report it as new either. A high-recall,
low-precision detector is exactly the right instrument for a delta gate and
exactly the wrong one for an absolute gate.

Two things follow that read oddly until the model is clear:

- An approved finding that **disappears** is not an event. The gate asks what is
  new, not what changed.
- Content drift with no new finding is reported, not blocked. Blocking on drift
  would make every whitespace edit a gate failure, and would push people back
  toward blanket bypasses.

### 2. The delta gate is the default; SkillSpector remains selectable

`skillgate` is the default. `--skill-gate skillspector` restores the previous
behaviour, and `DOTPACK_SKILL_GATE` sets it for a shell.

**This is a breaking change**, and deliberately so: the first install of any
package now blocks as a first sighting until it is approved with
`dotpack approve-skill`. That is the point of a delta gate — nothing about an
unreviewed package is trusted — but it changes what a fresh clone does, so it
lands in a minor release and is called out in the changelog and the README.

SkillSpector is kept rather than deleted. It is a second detector with different
coverage, its YAML baselines are still valid, and ADR-0015's reasoning about
native scanning still holds. A future `skillgate-both` gate could apply delta
semantics over both detectors' findings for strictly higher recall; the
`Gate` interface and the finding pipeline are shaped so that is additive. It is
not the default today because it would make every user provision two Python
environments on first run.

### 3. Gates are selected from an open registry

`internal/skillgate/registry` follows the adapter registry from
[ADR-0014](./0014-open-adapter-and-merge-backend-registries.md): gates register
from their own `init()`, one blank import in `internal/skillgate/all` wires them
in, and the enforcement funnel names no concrete gate. `Validate()` runs from
`internal/cli`'s `init()` and fails at process start if the default gate is not
registered.

`Build` rejects an unknown name rather than falling back to the default. A
typo'd `--skill-gate` must not quietly run a different gate than the operator
asked for, because they would believe a gate ran that did not.

### 4. Gate selection is operator-controlled, never package-controlled

Selection comes from `--skill-gate`, then `DOTPACK_SKILL_GATE`, then the
registry default — and from nowhere else. It is never read from the package
being installed, its policy root, or any file in a source tree.

A source that could choose its own gate could choose the weakest one, which
would make every other control in this ADR decorative.

### 5. A policy root is trusted only if dotpack did not fetch it

`resolveMaybeRemoteSource` clones a `github:` source into `DotpackHome` and
returns that path as the source root. The previous baseline discovery read
suppressions straight from the source root, so **a remote repository could ship
its own suppressions and silence findings about itself.** That hole predates
this ADR and applies to the incumbent gate.

The funnel now resolves the policy root once and marks anything under
`DotpackHome` untrusted. Both gates refuse approvals and suppressions from an
untrusted root, so a fetched package is evaluated against the *installing*
repository's approvals or, absent those, as a first sighting.

### 6. Baselines are tamper-evident, not tamper-proof

Approvals live at `<policy-root>/.dotpack/skillgate/baselines/<skill>.json` —
plain JSON, committed, reviewed in the pull-request diff. This follows
ADR-0015 §5's position that gate policy should be reviewable as ordinary files.

Each baseline records `detector_version`, `policy_version`, `tool_version`,
`approved_at` and an optional reason.

**We are not claiming more than that.** Anyone who can land a commit can approve
a finding — exactly as anyone who can land a commit can add the code the finding
describes. Cryptographic signing was considered and rejected: it needs key
distribution, and an adopter with no key configured would fall back to unsigned,
so the guarantee would hold only where it was least needed. What provenance buys
is that an approval cannot be *silent*: the diff names the detector, the policy
and the moment.

A machine-local record under `DotpackHome` additionally notes what this machine
last installed against, so a baseline edited elsewhere is surfaced. It is
advisory and never blocks.

### 7. Provenance skew warns, it does not block

A detector or policy pin bump changes fingerprints across the estate at once.
Blocking on that would fail every package simultaneously, and the practical
response to a fleet-wide outage is a blanket bypass — the failure mode this ADR
exists to prevent. Skew is reported as drift, loudly, and the operator
re-approves at their own pace.

### 8. The invisible-character check stays in-process

Zero-width characters, bidi controls, and Cyrillic/Greek homoglyphs in
`SKILL.md` are checked deterministically by dotpack itself, not delegated to the
detector.

This class **cannot** be delegated to a semantic analyser: an invisible
codepoint is invisible in the analyser's input too. Measured against the source
implementation, the detector with its LLM engine enabled still missed 21
zero-width spaces in a real skill.

### 9. Policy is embedded, not repo-readable

The exclusion allowlist and the scanned-suffix list decide what the content
tripwire covers and which files are searched for hidden codepoints. A package
that could edit either could exclude itself from both. The policy document is
`go:embed`-ed, following the precedent of `internal/cli/lifecycle_tasks.yaml`.

## Consequences

**Baselines must be created before packages install.** For an existing estate
that is a one-time `dotpack approve-skill --all` per repository, reviewed as a
single diff. There is no migration from SkillSpector's YAML baselines: they
record a different detector's findings under different identities.

**Two Python runtimes may exist** under `DotpackHome` — `skillgate/runtime` and
`skillspector/runtime` — if both gates are used on one machine. Each is
provisioned lazily, so an operator who never selects the other never pays for it.

**Offline machines cannot provision either gate.** Both fail closed. The escapes
are `--skill-gate` and `--skill-bypass-security`, both explicit and both
reported to stdout. A silent offline degradation would be a worse answer.

**The `skillspector` gate is registered from `internal/cli` rather than its own
package, and that is debt.** The implementation it wraps —
`runSkillScansWithOptionalBaselines`, `buildSkillScanOutput`,
`writeSkillScanOutput` and the result structs in `skills_scan.go` — is shared
with the `scan-skills` and `baseline-skills` commands. Extracting roughly 450
lines out of the repository's most test-covered path, inside a change that
already introduces a security gate, is the wrong risk to take at once. The
extension point is unaffected: a third gate needs no edit to CLI core. The shim
also ignores its context, because `internal/skillspector` has no timeout
anywhere; the new detector runtime does.

**The gate's fingerprint and content-hash definitions are now compatibility
surface.** If either changes, every baseline in every adopter's repository
silently stops matching. Both are pinned by golden vectors cross-checked against
the reference implementation, so a change becomes a test failure rather than a
field mystery.
