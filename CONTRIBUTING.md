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
go build ./cmd/dotpack
mkdocs build --strict --site-dir /tmp/dotpack-site
gitleaks detect --source . --no-banner --redact
```

Install docs dependencies with:

```sh
pip3 install -r docs/requirements.txt
```

## Pull Requests

- Keep changes focused and explain behavior changes in the PR description.
- Update tests for behavior changes.
- Update README, CLI help, and schema docs when command behavior changes.
- Do not commit generated local host output such as `.agents`, `.claude`,
  `.gemini`, `.codex`, or `.antigravity`.
- Do not include private paths, credentials, or organization-specific examples
  in public docs or fixtures.

## Optional Sponsio Hardening

dotpack includes an optional post-install lifecycle hook for Sponsio-based
runtime guard verification. It is recommended for teams that use Sponsio, but
it is not required for dotpack itself. Use `--run-lifecycle` only after Sponsio
is installed and configured for your environment.

## Contribution Licensing

Unless you explicitly state otherwise, contributions submitted to this project
are licensed under the Apache License 2.0, matching the project license.
