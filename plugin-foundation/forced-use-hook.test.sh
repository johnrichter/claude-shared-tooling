#!/bin/bash
# forced-use-hook.test.sh -- tests for forced-use-hook.sh against the frozen
# testdata/routing-rules.json fixture.
#
# Usage: bash forced-use-hook.test.sh

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly HOOK="${SCRIPT_DIR}/forced-use-hook.sh"
readonly ROUTING_RULES="${SCRIPT_DIR}/testdata/routing-rules.json"

TESTS_RUN=0
TESTS_FAILED=0

pass() {
  TESTS_RUN=$((TESTS_RUN + 1))
  echo "PASS: $1"
}

fail() {
  TESTS_RUN=$((TESTS_RUN + 1))
  TESTS_FAILED=$((TESTS_FAILED + 1))
  echo "FAIL: $1"
}

run_hook() {
  # $1 = JSON payload, remaining args = env assignments (NAME=value)
  local payload="$1"
  shift
  printf '%s' "${payload}" | env "$@" PF_ROUTING_RULES="${ROUTING_RULES}" "${HOOK}"
}

log_file="$(mktemp)"
readonly log_file

# Test 1: raw Bash usage of a governed operation, CLI available (via
# EXAMPLE_CLI_BIN) -- denies with a redirect message naming the usage hint,
# never claiming git doesn't exist. Logs outcome "fired".
: >"${log_file}"
out="$(
  run_hook '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"git status --short"}}' \
    EXAMPLE_CLI_BIN=/bin/true PF_HOOKEVAL_LOG="${log_file}"
)"
if printf '%s' "${out}" | jq -e '.hookSpecificOutput.permissionDecision == "deny"' >/dev/null 2>&1 \
  && printf '%s' "${out}" | jq -e '.hookSpecificOutput.permissionDecisionReason | contains("example-cli status")' >/dev/null 2>&1; then
  pass "raw_bash_usage_with_cli_available_denies_and_redirects"
else
  fail "raw_bash_usage_with_cli_available_denies_and_redirects (out='${out}')"
fi
if jq -e 'select(.operation == "status" and .outcome == "fired" and .denies_tool_exists == false)' "${log_file}" >/dev/null 2>&1; then
  pass "fired_outcome_logged_without_denying_tool_exists"
else
  fail "fired_outcome_logged_without_denying_tool_exists (log: $(cat "${log_file}"))"
fi

# Test 2: same raw usage, CLI unavailable -- fails open (no deny, no stdout),
# logs outcome "failed_open".
: >"${log_file}"
out2="$(
  PATH="/usr/bin:/bin" run_hook '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"git status --short"}}' \
    PF_HOOKEVAL_LOG="${log_file}"
)"
if [[ -z "${out2}" ]]; then
  pass "raw_bash_usage_with_cli_unavailable_fails_open"
else
  fail "raw_bash_usage_with_cli_unavailable_fails_open (out='${out2}')"
fi
if jq -e 'select(.operation == "status" and .outcome == "failed_open" and .denies_tool_exists == false)' "${log_file}" >/dev/null 2>&1; then
  pass "failed_open_outcome_logged_without_denying_tool_exists"
else
  fail "failed_open_outcome_logged_without_denying_tool_exists (log: $(cat "${log_file}"))"
fi

# Test 3: a Bash command that is already the sanctioned CLI invocation is
# not a raw-route match -- no deny, logged not_applicable, evaluation
# continues to (and logs) no other Bash-scoped operation.
: >"${log_file}"
out3="$(
  run_hook '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"example-cli status --json"}}' \
    EXAMPLE_CLI_BIN=/bin/true PF_HOOKEVAL_LOG="${log_file}"
)"
if [[ -z "${out3}" ]] && jq -e 'select(.operation == "status" and .outcome == "not_applicable")' "${log_file}" >/dev/null 2>&1; then
  pass "cli_usage_itself_is_not_applicable"
else
  fail "cli_usage_itself_is_not_applicable (out='${out3}', log: $(cat "${log_file}"))"
fi

# Test 4: a non-Bash raw route (Read) with no command_prefixes matches
# unconditionally -- denies when the CLI is available.
: >"${log_file}"
out4="$(
  run_hook '{"session_id":"s1","tool_name":"Read","tool_input":{"file_path":"/etc/example/config.yaml"}}' \
    EXAMPLE_CLI_BIN=/bin/true PF_HOOKEVAL_LOG="${log_file}"
)"
if printf '%s' "${out4}" | jq -e '.hookSpecificOutput.permissionDecision == "deny"' >/dev/null 2>&1 \
  && jq -e 'select(.operation == "config" and .outcome == "fired")' "${log_file}" >/dev/null 2>&1; then
  pass "non_bash_raw_route_with_no_prefixes_matches_unconditionally"
else
  fail "non_bash_raw_route_with_no_prefixes_matches_unconditionally (out='${out4}', log: $(cat "${log_file}"))"
fi

# Test 5: a tool no operation names is entirely irrelevant -- no stdout, no
# log entries at all.
: >"${log_file}"
out5="$(
  run_hook '{"session_id":"s1","tool_name":"Grep","tool_input":{"pattern":"foo"}}' \
    EXAMPLE_CLI_BIN=/bin/true PF_HOOKEVAL_LOG="${log_file}"
)"
if [[ -z "${out5}" && ! -s "${log_file}" ]]; then
  pass "unrelated_tool_is_silently_ignored"
else
  fail "unrelated_tool_is_silently_ignored (out='${out5}', log: $(cat "${log_file}"))"
fi

# Test 6: PF_ROUTING_RULES missing/unset degrades to a silent no-op.
: >"${log_file}"
out6="$(
  printf '%s' '{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"git status"}}' \
    | PF_HOOKEVAL_LOG="${log_file}" PF_ROUTING_RULES="/nonexistent/routing-rules.json" "${HOOK}"
)"
if [[ -z "${out6}" && ! -s "${log_file}" ]]; then
  pass "missing_routing_rules_is_silent_no_op"
else
  fail "missing_routing_rules_is_silent_no_op (out='${out6}', log: $(cat "${log_file}"))"
fi

rm -f "${log_file}"

echo ""
echo "Tests run: ${TESTS_RUN}"
echo "Tests failed: ${TESTS_FAILED}"
if [[ ${TESTS_FAILED} -eq 0 ]]; then
  echo "All tests passed."
  exit 0
else
  echo "Some tests failed."
  exit 1
fi
