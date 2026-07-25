#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/run-govulncheck.sh [--allow-vuln-db-submit] [-- <govulncheck arguments>]

govulncheck queries vuln.go.dev and may send dependency metadata derived from
the module graph. The default is not to submit that metadata.

Options:
  --allow-vuln-db-submit  Explicitly authorize the vuln.go.dev query.
  --help                  Show this help.

When no govulncheck arguments are supplied after "--", the wrapper scans ./....
EOF
}

allow_submit=false
govulncheck_args=()

while (($# > 0)); do
  case "$1" in
    --allow-vuln-db-submit)
      allow_submit=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --)
      shift
      govulncheck_args=("$@")
      break
      ;;
    *)
      printf 'error: unknown argument: %s\n\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$allow_submit" != true ]]; then
  cat >&2 <<'EOF'
govulncheck was not run because querying vuln.go.dev may submit dependency
metadata. The default is not to submit.

Offer the user this opt-in at the end of the run. After explicit approval, run:
  scripts/run-govulncheck.sh --allow-vuln-db-submit

For an autonomous run with pre-authorized submission, pass the same flag.
EOF
  exit 2
fi

if ((${#govulncheck_args[@]} == 0)); then
  govulncheck_args=(./...)
fi

govulncheck_bin="${GOVULNCHECK_BIN:-govulncheck}"
exec "$govulncheck_bin" "${govulncheck_args[@]}"
