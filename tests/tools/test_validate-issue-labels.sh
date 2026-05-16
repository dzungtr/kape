#!/usr/bin/env bash
# tests/tools/test_validate-issue-labels.sh
# Pure-bash tests for tools/validate-issue-labels.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/_lib.sh"

# Helper: feed a JSON blob to the validator and capture stdout.
run_validator() {
  local input="$1"
  echo "$input" | bash "$ROOT/tools/validate-issue-labels.sh"
}

# --- tests ---

test_two_commitments_error() {
  local result
  result="$(run_validator '{"number":1,"body":"","labels":[{"name":"committed"},{"name":"stretch"}]}')"
  assert_contains "$result" "ERROR multiple-commitments" "two commitments must error" || return 1
}

test_ready_without_category() {
  local result
  result="$(run_validator '{"number":2,"body":"","labels":[{"name":"ready"},{"name":"area/operator"},{"name":"backlog"}]}')"
  assert_contains "$result" "ERROR ready-missing-category" "ready without category must error" || return 1
}

test_ready_without_area() {
  local result
  result="$(run_validator '{"number":3,"body":"","labels":[{"name":"ready"},{"name":"bug"},{"name":"backlog"}]}')"
  assert_contains "$result" "ERROR ready-missing-area" "ready without area must error" || return 1
}

test_ready_without_commitment() {
  local result
  result="$(run_validator '{"number":4,"body":"","labels":[{"name":"ready"},{"name":"bug"},{"name":"area/operator"}]}')"
  assert_contains "$result" "ERROR ready-missing-commitment" "ready without commitment must error" || return 1
}

test_ready_with_needs() {
  local result
  result="$(run_validator '{"number":5,"body":"","labels":[{"name":"ready"},{"name":"bug"},{"name":"area/operator"},{"name":"backlog"},{"name":"needs/info"}]}')"
  assert_contains "$result" "ERROR ready-with-needs" "ready with needs/* must error" || return 1
}

test_both_triage_and_ready() {
  local result
  result="$(run_validator '{"number":6,"body":"","labels":[{"name":"ready"},{"name":"needs-triage"},{"name":"bug"},{"name":"area/operator"},{"name":"backlog"}]}')"
  assert_contains "$result" "ERROR both-needs-triage-and-ready" "both labels must error" || return 1
}

test_urgent_without_reason_warns() {
  local result
  result="$(run_validator '{"number":7,"body":"some text without the marker","labels":[{"name":"urgent"}]}')"
  assert_contains "$result" "WARN urgent-without-reason" "urgent w/o reason must warn" || return 1
}

test_urgent_with_reason_no_warn() {
  local result
  result="$(run_validator '{"number":8,"body":"Reason: prod incident\nmore text","labels":[{"name":"urgent"}]}')"
  assert_not_contains "$result" "urgent-without-reason" "urgent w/ reason must not warn" || return 1
}

test_blocked_without_reference_warns() {
  local result
  result="$(run_validator '{"number":9,"body":"some text","labels":[{"name":"blocked"}]}')"
  assert_contains "$result" "WARN blocked-without-reference" "blocked w/o reference must warn" || return 1
}

test_blocked_with_reference_no_warn() {
  local result
  result="$(run_validator '{"number":10,"body":"Blocked by: #42","labels":[{"name":"blocked"}]}')"
  assert_not_contains "$result" "blocked-without-reference" "blocked w/ reference must not warn" || return 1
}

test_clean_issue_silent() {
  local result
  result="$(run_validator '{"number":11,"body":"","labels":[{"name":"bug"},{"name":"area/operator"},{"name":"backlog"},{"name":"ready"}]}')"
  assert_empty "$result" "clean issue must produce no output" || return 1
}

# --- runner ---

echo "Running $(basename "${BASH_SOURCE[0]}")"
echo
run_test "issue with two commitments → ERROR"             test_two_commitments_error
run_test "ready without category → ERROR"                 test_ready_without_category
run_test "ready without area → ERROR"                     test_ready_without_area
run_test "ready without commitment → ERROR"               test_ready_without_commitment
run_test "ready with needs/* → ERROR"                     test_ready_with_needs
run_test "both needs-triage and ready → ERROR"            test_both_triage_and_ready
run_test "urgent without Reason: → WARN"                  test_urgent_without_reason_warns
run_test "urgent with Reason: → no warn"                  test_urgent_with_reason_no_warn
run_test "blocked without Blocked by: → WARN"             test_blocked_without_reference_warns
run_test "blocked with Blocked by: → no warn"             test_blocked_with_reference_no_warn
run_test "clean issue → no output"                        test_clean_issue_silent
summary
