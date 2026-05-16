#!/usr/bin/env bash
# tests/tools/test_sync-labels.sh
# Pure-bash tests for tools/sync-labels.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
source "$SCRIPT_DIR/_lib.sh"

TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

# Fixture: minimal 2-label manifest
cat > "$TEST_DIR/labels.yaml" <<'EOF'
- name: bug
  color: d73a4a
  description: "Defect"
- name: area/operator
  color: 0e8a16
  description: "Operator"
EOF

# --- tests ---

test_dry_run_create() {
  export SYNC_LABELS_GH_LIST_FIXTURE='[]'
  local output rc
  output="$(bash "$ROOT/tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml" 2>&1)"
  rc=$?
  assert_status 0 "$rc" "script exit code" || return 1
  assert_contains "$output" "CREATE bug" "should plan to create bug" || return 1
  assert_contains "$output" "CREATE area/operator" "should plan to create area/operator" || return 1
}

test_dry_run_update() {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"bug","color":"000000","description":"Defect"}]'
  local output rc
  output="$(bash "$ROOT/tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml" 2>&1)"
  rc=$?
  assert_status 0 "$rc" "script exit code" || return 1
  assert_contains "$output" "UPDATE bug" "should plan to update bug (color changed)" || return 1
}

test_dry_run_delete() {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"bug","color":"d73a4a","description":"Defect"},{"name":"area/operator","color":"0e8a16","description":"Operator"},{"name":"obsolete","color":"ffffff","description":"old"}]'
  local output rc
  output="$(bash "$ROOT/tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml" 2>&1)"
  rc=$?
  assert_status 0 "$rc" "script exit code" || return 1
  assert_contains "$output" "DELETE obsolete" "should plan to delete labels not in manifest" || return 1
}

test_keep_extras_suppresses_delete() {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"obsolete","color":"ffffff","description":"old"}]'
  local output rc
  output="$(bash "$ROOT/tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml" --keep-extras 2>&1)"
  rc=$?
  assert_status 0 "$rc" "script exit code" || return 1
  assert_not_contains "$output" "DELETE" "--keep-extras should suppress deletes" || return 1
}

test_no_op_when_matched() {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"bug","color":"d73a4a","description":"Defect"},{"name":"area/operator","color":"0e8a16","description":"Operator"}]'
  local output rc
  output="$(bash "$ROOT/tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml" 2>&1)"
  rc=$?
  assert_status 0 "$rc" "script exit code" || return 1
  assert_not_contains "$output" "CREATE" "no creates expected" || return 1
  assert_not_contains "$output" "UPDATE" "no updates expected" || return 1
  assert_not_contains "$output" "DELETE" "no deletes expected" || return 1
}

# --- runner ---

echo "Running $(basename "${BASH_SOURCE[0]}")"
echo
run_test "dry-run plans creates for missing labels"        test_dry_run_create
run_test "dry-run plans updates on color/description diff" test_dry_run_update
run_test "dry-run plans deletes for labels not in manifest" test_dry_run_delete
run_test "--keep-extras suppresses deletes"                test_keep_extras_suppresses_delete
run_test "no-op when manifest matches gh exactly"          test_no_op_when_matched
summary
