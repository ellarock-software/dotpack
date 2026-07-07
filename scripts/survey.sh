#!/usr/bin/env bash
# scripts/survey.sh
#
# Phase 0: derive per-kind schemas empirically from a curated corpus.
#
# For each kind in schema-corpus.yaml:
#   1. Fetch each example from its GitHub source (raw.githubusercontent.com)
#   2. Stage the examples in a workdir under input/
#   3. Write a prompt file describing the survey task
#   4. Invoke $DOTPACK_AGENT_CMD <workdir>  (per ADR-0004 workdir handoff)
#   5. Read the proposed schema from workdir/output/schema.yaml
#   6. Print a diff vs any existing schema/<kind>.yaml for human review
#
# Usage:
#   DOTPACK_AGENT_CMD="gemini -p" ./scripts/survey.sh            # all kinds
#   DOTPACK_AGENT_CMD="codex -p" ./scripts/survey.sh skill hook  # specific kinds
#
# Requires: bash, curl, yq, jq
# Does NOT auto-commit anything. Human reviews output, then commits schema/<kind>.yaml
# and an ADR documenting the per-kind survey.

set -euo pipefail

CORPUS="${CORPUS:-schema-corpus.yaml}"
SCHEMA_DIR="${SCHEMA_DIR:-schema}"
WORKDIR_ROOT="${WORKDIR_ROOT:-.dotpack-workdirs/survey}"

if [[ -z "${DOTPACK_AGENT_CMD:-}" ]]; then
  echo "error: \$DOTPACK_AGENT_CMD is not set (see ADR-0002)" >&2
  echo "  e.g. export DOTPACK_AGENT_CMD='gemini -p'" >&2
  exit 1
fi

for dep in yq jq curl; do
  if ! command -v "$dep" >/dev/null 2>&1; then
    echo "error: missing dependency: $dep" >&2
    exit 1
  fi
done

mkdir -p "$SCHEMA_DIR" "$WORKDIR_ROOT"

kinds_to_survey=("$@")
if [[ ${#kinds_to_survey[@]} -eq 0 ]]; then
  mapfile -t kinds_to_survey < <(yq -r '.kinds | keys[]' "$CORPUS")
fi

# Parse <owner>/<repo>@<ref>#<path> into raw.githubusercontent.com URL
to_raw_url() {
  local src="$1"
  local owner_repo="${src%@*}"
  local rest="${src#*@}"
  local ref="${rest%%#*}"
  local path="${rest#*#}"
  echo "https://raw.githubusercontent.com/${owner_repo}/${ref}/${path}"
}

survey_kind() {
  local kind="$1"
  local workdir="$WORKDIR_ROOT/$kind"
  rm -rf "$workdir"
  mkdir -p "$workdir/input" "$workdir/output"

  local shape
  shape=$(yq -r ".kinds.\"$kind\".fixture_shape" "$CORPUS")

  # Fetch each example into workdir/input/<idx>--<basename>
  local idx=0
  while read -r src extract note; do
    [[ "$src" == "null" ]] && continue
    [[ "$src" == TODO/* ]] && { echo "skip TODO: $src"; continue; }
    idx=$((idx + 1))
    local url
    url=$(to_raw_url "$src")
    local basename="${src##*/}"
    local dest="$workdir/input/${idx}--${basename}"
    echo "[$kind] fetch $url -> $dest"
    if ! curl -fsSL "$url" -o "$dest"; then
      echo "  WARN: fetch failed, skipping" >&2
      continue
    fi
    # For fragment-shaped kinds, extract the relevant slice
    if [[ "$shape" == "config_fragment" && "$extract" != "null" ]]; then
      case "$basename" in
        *.json)
          jq "$extract" "$dest" > "$dest.fragment" && mv "$dest.fragment" "$dest"
          ;;
        *.toml)
          # yq (mikefarah) v4 supports TOML via `-p toml`; output JSON so the
          # agent sees a uniform fragment shape regardless of source format.
          if yq -p toml -o json "$extract" "$dest" > "$dest.fragment" 2>"$dest.fragment.err"; then
            mv "$dest.fragment" "$dest"
            rm -f "$dest.fragment.err"
          else
            echo "  WARN: yq TOML extraction failed for $dest (extract=$extract):" >&2
            sed 's/^/    /' "$dest.fragment.err" >&2
            rm -f "$dest.fragment" "$dest.fragment.err"
          fi
          ;;
      esac
    fi
  done < <(yq -r ".kinds.\"$kind\".examples[] | [.source, (.extract // \"null\"), (.note // \"\")] | @tsv" "$CORPUS")

  if [[ $idx -eq 0 ]]; then
    echo "[$kind] no examples fetched (all TODO?); skipping" >&2
    return
  fi

  # Write the survey prompt
  cat > "$workdir/prompt.md" <<EOF
# Survey task: propose a schema for dotpack kind "$kind"

You are surveying $idx real-world examples of dotpack kind \`$kind\`
(fixture shape: \`$shape\`). The examples are staged in \`input/\`.

For each example:
- If shape is \`file_with_frontmatter\`: parse YAML frontmatter at the top
- If shape is \`config_fragment\`: parse the JSON/TOML fragment
- If shape is \`full_file\`: identify recurring structural sections

Cluster the fields/sections across all examples. Produce a proposed schema
at \`output/schema.yaml\` with this shape:

\`\`\`yaml
kind: $kind
dotpack_schema_version: 0
fields:
  - name: <field-name>
    type: <string | bool | int | array<...> | object>
    required: <true|false>
    appears_in: <count>/$idx examples
    notes: <where it diverges, aliases seen, etc.>
ecosystem_notes:
  - <any cross-tool divergence worth recording, e.g. "Gemini commands use TOML, Claude uses markdown+YAML">
\`\`\`

Also write \`output/clusters.md\` with a brief explanation of how you
grouped fields and which decisions you made (e.g., "saw both 'event' and
'on' — recommend 'on' as canonical, alias 'event'").

Do not include any field that appears in fewer than 2 examples unless its
absence would prevent the kind from being installable (e.g., a hook with
no command can't run). If you choose to include a sub-floor field, the
\`clusters.md\` entry for it must state *the concrete installation failure
mode* that omitting it would cause — vibes-level justifications ("aligns
with package-manager patterns", "seems like a bucket") are not acceptable.

When you write \`appears_in\` counts, verify them by inspecting the input
files directly (e.g. grep for the field name across \`input/*\`). The count
must match the actual occurrences, not your impression after skimming.

Fields present in fewer than 100% of examples are OPTIONAL by default
(\`required: false\`). Marking a sub-100% field as required requires
*corpus evidence* that omission breaks installation in this host — not a
hypothetical "non-X agents would be chatter-only" scenario. If 3 out of 8
examples omit a field and are still real, working, in-the-wild installs,
the field is optional, full stop.

For \`config_fragment\` shapes where the fragment is a MAP whose values
share the same shape (e.g. \`.mcpServers\` keyed by server name, \`.hooks\`
keyed by event name), count field presence across the *entries* (the map
values), not across files. A corpus of 3 files with 16 server entries
gives 16 as the denominator, not 3. Per-file counts under-represent the
real adoption rate of fields like \`env\` that may concentrate in a single
file but cover many entries within it.
EOF

  echo "[$kind] invoking \$DOTPACK_AGENT_CMD on $workdir"
  # ADR-0004 contract: agent reads workdir/{prompt.md, input/}, writes workdir/output/
  $DOTPACK_AGENT_CMD "$workdir"

  if [[ ! -f "$workdir/output/schema.yaml" ]]; then
    echo "[$kind] ERROR: agent did not write output/schema.yaml" >&2
    return 1
  fi

  echo "[$kind] proposed schema:"
  cat "$workdir/output/schema.yaml"
  if [[ -f "$SCHEMA_DIR/$kind.yaml" ]]; then
    echo "[$kind] diff vs existing schema/$kind.yaml:"
    diff -u "$SCHEMA_DIR/$kind.yaml" "$workdir/output/schema.yaml" || true
  fi
  echo "[$kind] review at $workdir/output/, then: cp $workdir/output/schema.yaml $SCHEMA_DIR/$kind.yaml"
}

for kind in "${kinds_to_survey[@]}"; do
  survey_kind "$kind"
done
