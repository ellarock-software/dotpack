#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
wrapper="$repo_root/scripts/run-govulncheck.sh"
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT

fake_govulncheck="$test_dir/govulncheck"
invocation_log="$test_dir/invocations"

printf '%s\n' \
  '#!/usr/bin/env bash' \
  'printf "%s\n" "$*" >> "$INVOCATION_LOG"' \
  > "$fake_govulncheck"
chmod +x "$fake_govulncheck"

run_wrapper() {
  INVOCATION_LOG="$invocation_log" \
    GOVULNCHECK_BIN="$fake_govulncheck" \
    "$wrapper" "$@"
}

set +e
run_wrapper >"$test_dir/default.out" 2>&1
default_status=$?
set -e
if [[ "$default_status" -ne 2 ]]; then
  printf 'expected default invocation to exit 2, got %s\n' "$default_status" >&2
  exit 1
fi
if [[ -e "$invocation_log" ]]; then
  printf 'default invocation unexpectedly called govulncheck\n' >&2
  exit 1
fi

run_wrapper --help >"$test_dir/help.out"
if [[ -e "$invocation_log" ]]; then
  printf 'help invocation unexpectedly called govulncheck\n' >&2
  exit 1
fi

set +e
run_wrapper --unknown >"$test_dir/unknown.out" 2>&1
unknown_status=$?
set -e
if [[ "$unknown_status" -ne 2 ]]; then
  printf 'expected unknown argument to exit 2, got %s\n' "$unknown_status" >&2
  exit 1
fi
if [[ -e "$invocation_log" ]]; then
  printf 'unknown argument unexpectedly called govulncheck\n' >&2
  exit 1
fi

run_wrapper --allow-vuln-db-submit
if [[ "$(cat "$invocation_log")" != "./..." ]]; then
  printf 'opt-in invocation did not use the default ./... target\n' >&2
  exit 1
fi

run_wrapper --allow-vuln-db-submit -- -show verbose ./cmd/...
if [[ "$(tail -n 1 "$invocation_log")" != "-show verbose ./cmd/..." ]]; then
  printf 'opt-in invocation did not forward explicit arguments\n' >&2
  exit 1
fi

printf 'run-govulncheck consent tests passed\n'
