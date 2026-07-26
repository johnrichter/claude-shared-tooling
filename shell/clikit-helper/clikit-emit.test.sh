#!/bin/bash
# clikit-emit.test.sh — tests for the clikit shell helper.
#
# These tests verify that clikit-emit delegates to the emitter (when available)
# and produces clikit records without hand-writing JSON.
#
# Usage: bash clikit-emit.test.sh

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly CLIKIT_EMIT="${SCRIPT_DIR}/clikit-emit"

# Path to the clikit binary. In production, this resolves to the real emitter.
export CLIKIT_BIN="${SCRIPT_DIR}/../../go/.bin/clikit"

# Test counter and failure tracking.
TESTS_RUN=0
TESTS_FAILED=0

# test_case NAME COMMAND EXPECTED_EXIT
# Run a test case and verify the exit code.
test_case() {
  local name="$1"
  local command="$2"
  local expected_exit="$3"

  TESTS_RUN=$((TESTS_RUN + 1))

  local actual_exit
  if eval "$command" > /dev/null 2>&1; then
    actual_exit=0
  else
    actual_exit=$?
  fi

  if [[ $actual_exit -eq $expected_exit ]]; then
    echo "PASS: $name"
    return 0
  else
    echo "FAIL: $name (expected exit $expected_exit, got $actual_exit)"
    TESTS_FAILED=$((TESTS_FAILED + 1))
    return 1
  fi
}

# Test 1: When the emitter binary is missing, the helper exits 90 and writes
# NOTHING to stdout — it never hand-writes a JSON record. The diagnostic goes to
# stderr, which is not part of the result.
TESTS_RUN=$((TESTS_RUN + 1))
t1_stdout=""
t1_exit=0
t1_stdout="$(CLIKIT_BIN=/nonexistent/clikit "$CLIKIT_EMIT" --command test --status success --exit-code 0 2>/dev/null)" || t1_exit=$?
if [[ $t1_exit -eq 90 && -z "$t1_stdout" ]]; then
  echo "PASS: missing_emitter_exits_90_with_empty_stdout"
else
  echo "FAIL: missing_emitter_exits_90_with_empty_stdout (exit=$t1_exit, stdout='$t1_stdout')"
  TESTS_FAILED=$((TESTS_FAILED + 1))
fi

# Test 2: Helper delegates to the emitter when available.
# This test only runs if the clikit binary is built.
if [[ -x "$CLIKIT_BIN" ]]; then
  # Success record should exit with the specified code.
  test_case \
    "success_record_exits_0_via_emitter" \
    "$CLIKIT_EMIT --command test,cmd --status success --exit-code 0" \
    0

  # Conflict should exit with the specified code.
  test_case \
    "conflict_record_exits_41_via_emitter" \
    "$CLIKIT_EMIT --command test,cmd --status conflict --exit-code 41 --error test.err 'message'" \
    41

  # The record on stdout is produced by the emitter and is canonical JSON with
  # RFC 8785 lexicographically-ordered keys (the property the helper must never
  # violate by hand-writing).
  TESTS_RUN=$((TESTS_RUN + 1))
  emitted="$("$CLIKIT_EMIT" --command test,cmd --status success --exit-code 0 2>/dev/null || true)"
  if printf '%s' "$emitted" | python3 -c '
import json,sys
raw=sys.stdin.read()
obj=json.loads(raw)
def canon(o):
    if isinstance(o,dict):
        return "{"+",".join(json.dumps(k)+":"+canon(v) for k,v in sorted(o.items()))+"}"
    if isinstance(o,list):
        return "["+",".join(canon(v) for v in o)+"]"
    return json.dumps(o,separators=(",",":"),ensure_ascii=False)
sys.exit(0 if raw.strip()==canon(obj) else 1)
'; then
    echo "PASS: emitter_output_is_canonical_json"
  else
    echo "FAIL: emitter_output_is_canonical_json (stdout not RFC 8785 canonical: '$emitted')"
    TESTS_FAILED=$((TESTS_FAILED + 1))
  fi
else
  echo "SKIP: clikit emitter binary not found at $CLIKIT_BIN"
  echo "      (integration tests require the emitter to be built)"
fi

# Summary.
echo ""
echo "Tests run: $TESTS_RUN"
echo "Tests failed: $TESTS_FAILED"

if [[ $TESTS_FAILED -eq 0 ]]; then
  echo "All tests passed."
  exit 0
else
  echo "Some tests failed."
  exit 1
fi
