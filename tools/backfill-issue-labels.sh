#!/usr/bin/env bash
# tools/backfill-issue-labels.sh
# Backfill the new label taxonomy on open issues that pre-date it.
# Dry-run by default; --apply does the mutations.
# Derivation helpers are duplicated from tools/migrate-to-github.sh by design:
# both are one-shot scripts, and a shared library is overkill for ~30 lines.
set -euo pipefail

APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1

derive_area() {
  local title="$1"
  local t
  t="$(echo "$title" | tr '[:upper:]' '[:lower:]')"
  case "$t" in
    *"operator"*|*"reconciler"*|*"kapehandler"*|*"kapetool"*|*"kapeschema"*|*"crd validation"*) echo "area/operator" ;;
    *"task-service"*|*"task service"*|*"openapi"*) echo "area/task-service" ;;
    *"adapter"*|*"alertmanager"*|*"audit adapter"*) echo "area/adapters" ;;
    *"kapeproxy"*|*"mcp proxy"*) echo "area/kapeproxy" ;;
    *"runtime"*|*"langgraph"*|*"python"*) echo "area/runtime" ;;
    *"dashboard"*|*"sse"*|*"eventsource"*|*"oauth2 proxy"*) echo "area/dashboard" ;;
    *"helm"*|*"chart"*|*"template"*) echo "area/helm" ;;
    *"crd"*|*"cel"*|*"xvalidation"*) echo "area/crds" ;;
    *"docs"*|*"readme"*|*"runbook"*|*"changelog"*) echo "area/docs" ;;
    *"ci"*|*"workflow"*|*"actions"*|*"snyk"*) echo "area/ci" ;;
    *"nats"*|*"postgres"*|*"cloudnativepg"*|*"cert-manager"*|*"eso"*|*"external secrets"*) echo "area/infra" ;;
    *) echo "" ;;
  esac
}

derive_phase() {
  local title="$1"
  case "$title" in
    \[P6/*) echo "phase/M2-operator" ;;
    \[P7/*) echo "phase/M3-runtime" ;;
    \[P8/*) echo "phase/M4-security" ;;
    \[P9/*) echo "phase/M5-dashboard" ;;
    \[P10/*) echo "phase/M6-release" ;;
    *) echo "" ;;
  esac
}

apply_labels() {
  local issue_num="$1" labels="$2"
  echo "[$issue_num] add: $labels"
  if [[ $APPLY -eq 1 ]]; then
    # gh issue edit --add-label expects comma-separated
    gh issue edit "$issue_num" --add-label "$labels"
  fi
}

main() {
  gh issue list --state open --limit 200 \
    --json number,title,labels --jq '.[] | @json' \
    | while read -r row; do
      local num title existing
      num=$(echo "$row" | jq -r '.number')
      title=$(echo "$row" | jq -r '.title')
      existing=$(echo "$row" | jq -r '[.labels[].name] | join(",")')

      # Skip if issue already has a commitment label (already migrated)
      if echo ",$existing," | grep -qE ',(committed|stretch|backlog),'; then
        continue
      fi

      local to_add=()
      local area phase
      area="$(derive_area "$title")"
      phase="$(derive_phase "$title")"

      # Ensure category for roadmap-tagged issues
      if echo ",$existing," | grep -q ',roadmap-sync,' && ! echo ",$existing," | grep -qE ',(bug|enhancement|feature|refactor|redesign|security|docs|chore|test|spec),'; then
        to_add+=("enhancement")
      fi

      [[ -n "$area" ]] && ! echo ",$existing," | grep -q ",$area," && to_add+=("$area")
      [[ -n "$phase" ]] && ! echo ",$existing," | grep -q ",$phase," && to_add+=("$phase")

      # Default commitment: committed if roadmap (assumed in current milestone), else backlog
      if echo ",$existing," | grep -q ',roadmap-sync,'; then
        to_add+=("committed")
      else
        to_add+=("backlog")
      fi

      # If area or phase couldn't be derived, mark needs-triage so the validator surfaces it
      if [[ -z "$area" || ( -z "$phase" && $(echo ",$existing," | grep -c ',roadmap-sync,') -eq 1 ) ]]; then
        to_add+=("needs-triage")
      fi

      if [[ ${#to_add[@]} -gt 0 ]]; then
        apply_labels "$num" "$(IFS=,; echo "${to_add[*]}")"
      fi
    done
}

main "$@"
