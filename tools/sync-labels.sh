#!/usr/bin/env bash
# tools/sync-labels.sh
# Idempotently sync the label set defined in .github/labels.yaml to GitHub.
# Usage:
#   tools/sync-labels.sh [--manifest <path>] [--keep-extras] [--apply]
# Modes:
#   default: dry-run (print planned mutations, no gh calls)
#   --apply: actually call gh label create/edit/delete
set -euo pipefail

MANIFEST=".github/labels.yaml"
APPLY=0
KEEP_EXTRAS=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifest) MANIFEST="$2"; shift 2 ;;
    --apply) APPLY=1; shift ;;
    --keep-extras) KEEP_EXTRAS=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Test hook: if SYNC_LABELS_DRY_RUN is set, force dry-run regardless of --apply.
[[ -n "${SYNC_LABELS_DRY_RUN:-}" ]] && APPLY=0 || true

# Test hook: if SYNC_LABELS_GH_LIST_FIXTURE is set, use it instead of real gh.
gh_list_labels() {
  if [[ -n "${SYNC_LABELS_GH_LIST_FIXTURE:-}" ]]; then
    echo "$SYNC_LABELS_GH_LIST_FIXTURE"
  else
    gh label list --json name,color,description --limit 200
  fi
}

# Parse manifest into TSV: name<TAB>color<TAB>description
manifest_tsv() {
  yq -o=json '.' "$MANIFEST" \
    | jq -r '.[] | [.name, .color, .description] | @tsv'
}

# Parse existing labels into the same TSV shape
existing_tsv() {
  gh_list_labels | jq -r '.[] | [.name, .color, .description] | @tsv'
}

main() {
  local manifest existing
  manifest="$(manifest_tsv)"
  existing="$(existing_tsv)"

  # Phase 1: create or update
  while IFS=$'\t' read -r name color desc; do
    [[ -z "$name" ]] && continue
    local current
    current="$(echo "$existing" | awk -F'\t' -v n="$name" '$1==n {print; exit}')" || true
    if [[ -z "$current" ]]; then
      echo "CREATE $name"
      [[ $APPLY -eq 1 ]] && gh label create "$name" --color "$color" --description "$desc" || true
    else
      local cur_color cur_desc
      cur_color="$(echo "$current" | cut -f2)"
      cur_desc="$(echo "$current" | cut -f3)"
      if [[ "$cur_color" != "$color" || "$cur_desc" != "$desc" ]]; then
        echo "UPDATE $name"
        [[ $APPLY -eq 1 ]] && gh label edit "$name" --color "$color" --description "$desc" || true
      fi
    fi
  done <<< "$manifest"

  # Phase 2: delete extras (unless --keep-extras)
  if [[ $KEEP_EXTRAS -eq 0 ]]; then
    while IFS=$'\t' read -r name _ _; do
      [[ -z "$name" ]] && continue
      if ! echo "$manifest" | awk -F'\t' -v n="$name" '$1==n {found=1} END {exit !found}'; then
        echo "DELETE $name"
        [[ $APPLY -eq 1 ]] && gh label delete "$name" --yes || true
      fi
    done <<< "$existing"
  fi
}

main "$@"
