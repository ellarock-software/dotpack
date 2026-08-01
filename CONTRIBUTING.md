# Contributing to dotpack

Thanks for taking the time to improve dotpack. This project is a Go CLI for
translating portable `.agents` resources into host-native agent configuration
files.

## Development Setup

Requirements:

- Go 1.26.3 or newer compatible toolchain
- Python 3 with `pip` for documentation builds
- `gitleaks` for local secret scanning

Run the core checks before opening a pull request:

```sh
gofmt -w $(git ls-files '*.go')
go mod tidy
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/dotpack
mkdocs build --strict --site-dir /tmp/dotpack-site
gitleaks git . --config .gitleaks.toml --redact --no-banner
go-licenses check ./... --disallowed_types=forbidden,restricted
bash scripts/run-govulncheck.test.sh
```

Install docs dependencies with:

```sh
pip3 install -r docs/requirements.txt
```

### Vulnerability scan consent

`govulncheck` queries `vuln.go.dev` and may send dependency metadata derived
from the module graph. Submission is opt-in:

```sh
scripts/run-govulncheck.sh
```

The default invocation does not contact the service and exits nonzero. At the
end of an interactive validation run, offer the user the option to authorize
the query. After explicit approval, run:

```sh
scripts/run-govulncheck.sh --allow-vuln-db-submit
```

Autonomous runs may pass `--allow-vuln-db-submit` only when submission was
authorized before the run. CI records that authorization explicitly in its
workflow.

## Adding a New Host Adapter

dotpack's intent is universal LLM-tool coverage. The adapter path is open
(see [ADR-0014](docs/adr/0014-open-adapter-and-merge-backend-registries.md)):
onboarding a host is self-contained and touches no core switchboard. Use the
`opencode` adapter as the worked example.

1. **Create the package** `internal/adapter/<host>/<host>.go`. Compose the shared
   deep modules: a `filedrop.Policy` for file-drop kinds (skill, agent, command,
   memory, rule) and a `configfrag.Policy` for merged-config kinds (mcp-server,
   hook). Support is *data*: a kind present in `Layouts`/`Kinds` is supported; an
   absent kind returns the standard `kind X not yet supported` error. Ship a
   partial matrix where the host has no concept for an operation — that is
   expected, not a workaround.
2. **Self-register** from an `init()`:
   `registry.RegisterAdapter(hostID, func(d dirs.Dirs) adapter.Adapter { return New(d) })`.
3. **Add a blank import** for the package to `internal/adapter/all/all.go`.
4. **Add a `<Host>Home` field** to `internal/dirs.Dirs` (struct field, `FromEnv`
   default + `DOTPACK_<HOST>_HOME` override + abs-normalization), if the host has
   a user-scope config root.
5. **Implement `DescribeLayouts()`** (optional but recommended) by delegating to
   `a.filedrop.DescribeLayouts()`, so reconcile's scan and CLI help pick the host
   up automatically. Append any non-filedrop layout (e.g. codex's TOML agent)
   manually.
6. **Add a `coverage_test.go`** mirroring an existing adapter's table test,
   asserting both supported paths and the `not yet supported` error for kinds the
   host lacks.
7. **Update docs**: the README support matrix and the `install`/root help (the
   help host list is registry-driven; the per-kind path table is hand-maintained).

The *only* remaining touch points outside your package are the single blank
import (step 3), the `dirs.Dirs` field (step 4), and docs (step 7). No edit to
`install.go`, `sync.go`, or the orchestrator is required — proven by
`internal/adapter/registry/registry_test.go`, which installs a fake host end to
end through the unchanged core.

### Adding a config-merge format

Merged config writes go through `mergeBackend` (see
`internal/orchestrator/mergebackends.go`). A new format is one interface
implementation plus a `registerBackend(".ext", backend{})` call. `.json`,
`.toml`, and `.yaml`/`.yml` ship today.

## Pull Requests

- Keep changes focused and explain behavior changes in the PR description.
- Update tests for behavior changes.
- Update README, CLI help, and schema docs when command behavior changes.
- Do not commit generated local host output such as `.agents`, `.claude`,
  `.gemini`, `.codex`, or `.antigravity`.
- Do not include private paths, credentials, or organization-specific examples
  in public docs or fixtures.

## SkillSpector Gating

dotpack includes a native SkillSpector path for scanning skill packages.

- Skill-bearing workflows automatically run a static SkillSpector gate before
  dotpack reads or materializes skill content.
- `dotpack scan-skills` runs the same static-only scan surface directly and
  gates by default.
- `dotpack baseline-skills` writes per-skill baseline YAML files that
  `scan-skills --baseline-dir ...` can apply later.
- Automatic gates look for baseline files under
  `<policy-root>/.dotpack/skillspector/baselines`, then fall back to a
  canonical agent-config gate at
  `<policy-root>/.agents/tools/skillspector-gate/baselines`.
- For automatic gates, a baseline is applied only to the individual skill that
  has a reviewed baseline file; unbaselined skills are still scanned and must
  pass with no findings.
- Skill-bearing commands accept repeatable
  `--skill-bypass-security <name>` arguments for explicit invocation-local
  exceptions. Bypasses match exact selected skill names, fail closed for
  unknown names, and are recorded in scan output.
- The SkillSpector runtime is provisioned under `DOTPACK_DOTPACK_HOME` and
  pinned to a specific upstream commit/version.

Use `scan-skills` when you want to inspect/export findings directly, and use
`baseline-skills` to author reviewed suppressions for the automatic gate.
Prefer a baseline over a full security bypass whenever the skill can still be
scanned.

## Contribution Licensing

Unless you explicitly state otherwise, contributions submitted to this project
are licensed under the Apache License 2.0, matching the project license. No
Contributor License Agreement (CLA) or Developer Certificate of Origin (DCO)
sign-off is required — opening a pull request is enough.
