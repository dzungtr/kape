#!/usr/bin/env bash
# tools/snyk-finding-to-issue.sh
# Open or update a GitHub issue from a Snyk MCP finding.
# Args:
#   --title <string>       (required)
#   --body  <string>       (required — should already include 'Reason: Snyk <severity> ...')
#   --scan-path <path>     (required — e.g., ./operator)
#   --finding-id <string>  (required — used to dedupe via title prefix)
set -euo pipefail

TITLE=""; BODY=""; SCAN_PATH=""; FINDING_ID=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --title) TITLE="$2"; shift 2 ;;
    --body) BODY="$2"; shift 2 ;;
    --scan-path) SCAN_PATH="$2"; shift 2 ;;
    --finding-id) FINDING_ID="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

for var in TITLE BODY SCAN_PATH FINDING_ID; do
  [[ -z "${!var}" ]] && { echo "missing --$(echo "$var" | tr '[:upper:]' '[:lower:]' | tr _ -)" >&2; exit 2; }
done

# Map scan path → area/*
area=""
case "$SCAN_PATH" in
  ./operator|operator) area="area/operator" ;;
  ./adapters|adapters) area="area/adapters" ;;
  ./task-service|task-service) area="area/task-service" ;;
  ./kapeproxy|kapeproxy) area="area/kapeproxy" ;;
  ./runtime|runtime) area="area/runtime" ;;
  *) area="" ;;
esac

# Prepend finding id to title so dedupe via title-match works
full_title="[$FINDING_ID] $TITLE"

existing=$(gh issue list --state all --search "in:title \"[$FINDING_ID]\"" --json number --jq '.[0].number // ""')
if [[ -n "$existing" ]]; then
  echo "Issue already exists: #$existing — skipping create"
  exit 0
fi

labels=("bug" "security" "snyk-finding" "urgent" "backlog")
[[ -n "$area" ]] && labels+=("$area")

gh issue create \
  --title "$full_title" \
  --body "$BODY" \
  $(printf -- "--label %s " "${labels[@]}")
