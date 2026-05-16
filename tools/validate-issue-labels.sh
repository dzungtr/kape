#!/usr/bin/env bash
# tools/validate-issue-labels.sh
# Read one issue's JSON from stdin (gh issue view --json number,body,labels output)
# and emit ERROR/WARN lines per docs/agent-rituals.md → "State validator".
# Exit code is always 0; aggregation is the caller's responsibility.
set -euo pipefail

input="$(cat)"

has_label() {
  local pattern="$1"
  echo "$input" | jq -e --arg p "$pattern" '.labels[] | select(.name == $p)' >/dev/null
}
count_labels_matching() {
  local pattern="$1"
  echo "$input" | jq --arg p "$pattern" '[.labels[].name | select(test($p))] | length'
}
body_has_line() {
  local pattern="$1"
  echo "$input" | jq -r '.body // ""' | grep -qE "$pattern"
}

categories='^(bug|enhancement|feature|refactor|redesign|security|docs|chore|test|spec)$'
areas='^area/'
needs='^needs/'

# Rule 1: multiple commitments
commitments=$(count_labels_matching '^(committed|stretch|backlog)$')
if [[ "$commitments" -gt 1 ]]; then
  echo "ERROR multiple-commitments: issue has $commitments commitment labels (expected 0 or 1)"
fi

# Rule 2-5: ready preconditions
if has_label "ready"; then
  cat_count=$(count_labels_matching "$categories")
  area_count=$(count_labels_matching "$areas")
  needs_count=$(count_labels_matching "$needs")
  [[ "$cat_count" -eq 0 ]] && echo "ERROR ready-missing-category: ready label set without a category label"
  [[ "$area_count" -eq 0 ]] && echo "ERROR ready-missing-area: ready label set without an area/* label"
  [[ "$commitments" -eq 0 ]] && echo "ERROR ready-missing-commitment: ready label set without a commitment label"
  [[ "$needs_count" -gt 0 ]] && echo "ERROR ready-with-needs: ready label set with open needs/* label(s)"
  has_label "needs-triage" && echo "ERROR both-needs-triage-and-ready: issue has both needs-triage and ready"
fi

# Rule 6-7: urgent / blocked body markers
if has_label "urgent" && ! body_has_line '^Reason:'; then
  echo "WARN urgent-without-reason: urgent label set but body missing 'Reason: <line>'"
fi
if has_label "blocked" && ! body_has_line '^Blocked by:'; then
  echo "WARN blocked-without-reference: blocked label set but body missing 'Blocked by: <ref>'"
fi
