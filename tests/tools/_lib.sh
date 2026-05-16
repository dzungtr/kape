#!/usr/bin/env bash
# tests/tools/_lib.sh
# Shared assertions and test driver for tests/tools/test_*.sh.
# Source this file; do not run it directly.
#
# Usage in a test file:
#   #!/usr/bin/env bash
#   set -uo pipefail
#   source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"
#   test_my_thing() { ... assert_contains ... ; }
#   run_test "my thing does X" test_my_thing
#   summary  # prints results and exits non-zero on failure

# Intentionally NO `set -e` here — failing assertions must not abort the
# runner; each test returns its status to run_test.

PASSED=0
FAILED=0
FAILURES=()

# Quote-truncate a value to 400 chars for readable failure output.
_trunc() { printf '%s' "$1" | head -c 400; }

assert_contains() {
  local haystack="$1" needle="$2" msg="${3:-assert_contains}"
  if [[ "$haystack" == *"$needle"* ]]; then
    return 0
  fi
  printf '    FAIL (%s): expected substring %q\n' "$msg" "$needle" >&2
  printf '    actual: %s\n' "$(_trunc "$haystack")" >&2
  return 1
}

assert_not_contains() {
  local haystack="$1" needle="$2" msg="${3:-assert_not_contains}"
  if [[ "$haystack" != *"$needle"* ]]; then
    return 0
  fi
  printf '    FAIL (%s): forbidden substring %q present\n' "$msg" "$needle" >&2
  printf '    actual: %s\n' "$(_trunc "$haystack")" >&2
  return 1
}

assert_empty() {
  local value="$1" msg="${2:-assert_empty}"
  if [[ -z "$value" ]]; then
    return 0
  fi
  printf '    FAIL (%s): expected empty\n' "$msg" >&2
  printf '    actual: %s\n' "$(_trunc "$value")" >&2
  return 1
}

assert_status() {
  local expected="$1" actual="$2" msg="${3:-assert_status}"
  if [[ "$expected" -eq "$actual" ]]; then
    return 0
  fi
  printf '    FAIL (%s): expected exit %d, got %d\n' "$msg" "$expected" "$actual" >&2
  return 1
}

# Run a named test function. The function returns 0 for pass, non-zero for fail.
# Each test runs in a subshell so env-var mutations and exported vars do not leak.
run_test() {
  local name="$1" fn="$2"
  printf '  %-60s ' "$name"
  if ( "$fn" ); then
    PASSED=$((PASSED+1))
    echo "PASS"
  else
    FAILED=$((FAILED+1))
    FAILURES+=("$name")
    echo "FAIL"
  fi
}

summary() {
  echo
  echo "=== Summary ==="
  echo "Passed: $PASSED"
  echo "Failed: $FAILED"
  if [[ $FAILED -gt 0 ]]; then
    printf '\nFailures:\n' >&2
    for f in "${FAILURES[@]}"; do
      printf '  - %s\n' "$f" >&2
    done
    exit 1
  fi
  exit 0
}
