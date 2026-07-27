#!/bin/bash
# download-script.test.sh -- integration tests for download-script.sh against
# the frozen testdata/release fixture, exercised entirely over file:// (no
# network, no real CLI binary).
#
# Usage: bash download-script.test.sh

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT="${SCRIPT_DIR}/download-script.sh"
readonly RELEASE_FIXTURE="${SCRIPT_DIR}/testdata/release"
readonly ARTIFACT="${RELEASE_FIXTURE}/example-cli-v1.0.0/example-cli-1.0.0-linux-x86_64"

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

fresh_data_dir() {
  mktemp -d
}

# Test 1: fresh cache -- fetches the artifact and its sidecar over file://,
# verifies the checksum, caches the binary, prints its path, exits 0.
data_dir="$(fresh_data_dir)"
out="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir}" \
    PF_RELEASE_BASE_URL="file://${RELEASE_FIXTURE}" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux-x86_64 \
    "${SCRIPT}" 2>/tmp/download-script.test.stderr
)" && exit_code=0 || exit_code=$?
if [[ ${exit_code} -eq 0 && "${out}" == "${data_dir}/bin/example-cli-1.0.0" && -f "${out}" && -f "${out}.sha256" ]]; then
  pass "fresh_fetch_verifies_and_caches"
else
  fail "fresh_fetch_verifies_and_caches (exit=${exit_code}, out='${out}')"
fi

if diff -q "${ARTIFACT}" "${data_dir}/bin/example-cli-1.0.0" >/dev/null 2>&1; then
  pass "cached_binary_matches_fixture_bytes"
else
  fail "cached_binary_matches_fixture_bytes"
fi

# Test 2: idempotent cache -- second run against an unreachable release host
# still succeeds, because the fast path re-verifies the cached binary against
# its own sidecar and never touches the network.
out2="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir}" \
    PF_RELEASE_BASE_URL="file:///nonexistent-release-host" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux-x86_64 \
    "${SCRIPT}" 2>/tmp/download-script.test.stderr
)" && exit_code2=0 || exit_code2=$?
if [[ ${exit_code2} -eq 0 && "${out2}" == "${out}" ]]; then
  pass "idempotent_cache_skips_network_on_second_run"
else
  fail "idempotent_cache_skips_network_on_second_run (exit=${exit_code2}, out='${out2}')"
fi

# Test 3: PF_VERSION_FILE resolves the pinned version from a plugin.json-
# shaped file's "version" field instead of a literal PF_VERSION.
data_dir3="$(fresh_data_dir)"
out3="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir3}" \
    PF_RELEASE_BASE_URL="file://${RELEASE_FIXTURE}" \
    PF_VERSION_FILE="${RELEASE_FIXTURE}/plugin.json" \
    PF_ARCH_OVERRIDE=linux-x86_64 \
    "${SCRIPT}" 2>/tmp/download-script.test.stderr
)" && exit_code3=0 || exit_code3=$?
if [[ ${exit_code3} -eq 0 && "${out3}" == "${data_dir3}/bin/example-cli-1.0.0" ]]; then
  pass "version_file_resolves_pinned_version"
else
  fail "version_file_resolves_pinned_version (exit=${exit_code3}, out='${out3}')"
fi

# Test 4: a checksum mismatch is a soft failure -- exit 1, no binary cached,
# nothing on stdout.
bad_fixture_dir="$(mktemp -d)"
mkdir -p "${bad_fixture_dir}/example-cli-v1.0.0"
cp "${ARTIFACT}" "${bad_fixture_dir}/example-cli-v1.0.0/example-cli-1.0.0-linux-x86_64"
echo "0000000000000000000000000000000000000000000000000000000000000000" >"${bad_fixture_dir}/example-cli-v1.0.0/example-cli-1.0.0-linux-x86_64.sha256"
data_dir4="$(fresh_data_dir)"
out4="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir4}" \
    PF_RELEASE_BASE_URL="file://${bad_fixture_dir}" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux-x86_64 \
    "${SCRIPT}" 2>/tmp/download-script.test.stderr
)" && exit_code4=0 || exit_code4=$?
if [[ ${exit_code4} -eq 1 && -z "${out4}" && ! -f "${data_dir4}/bin/example-cli-1.0.0" ]]; then
  pass "checksum_mismatch_is_soft_failure"
else
  fail "checksum_mismatch_is_soft_failure (exit=${exit_code4}, out='${out4}')"
fi
rm -rf "${bad_fixture_dir}"

# Test 5: an unreachable host with no prior cache is a soft failure, not a
# crash -- exit 1, no stdout.
data_dir5="$(fresh_data_dir)"
out5="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir5}" \
    PF_RELEASE_BASE_URL="file:///nonexistent-release-host" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux-x86_64 \
    "${SCRIPT}" 2>/tmp/download-script.test.stderr
)" && exit_code5=0 || exit_code5=$?
if [[ ${exit_code5} -eq 1 && -z "${out5}" ]]; then
  pass "unreachable_host_with_no_cache_is_soft_failure"
else
  fail "unreachable_host_with_no_cache_is_soft_failure (exit=${exit_code5}, out='${out5}')"
fi

# Test 6: a missing required env var is a misconfiguration, not a runtime
# provisioning outcome -- exit 2.
set +e
PF_PLUGIN_DATA="$(fresh_data_dir)" PF_RELEASE_BASE_URL="file://${RELEASE_FIXTURE}" PF_VERSION=1.0.0 "${SCRIPT}" >/tmp/download-script.test.stdout 2>/tmp/download-script.test.stderr
exit_code6=$?
set -e
if [[ ${exit_code6} -eq 2 ]]; then
  pass "missing_required_env_exits_2"
else
  fail "missing_required_env_exits_2 (exit=${exit_code6})"
fi

# Test 7: PF_ENV_FILE receives the export line on a verified run.
data_dir7="$(fresh_data_dir)"
env_file="$(mktemp)"
PF_CLI_NAME=example-cli \
  PF_PLUGIN_DATA="${data_dir7}" \
  PF_RELEASE_BASE_URL="file://${RELEASE_FIXTURE}" \
  PF_VERSION=1.0.0 \
  PF_ARCH_OVERRIDE=linux-x86_64 \
  PF_ENV_FILE="${env_file}" \
  "${SCRIPT}" >/dev/null 2>/tmp/download-script.test.stderr
if grep -q "^export EXAMPLE_CLI_BIN=\"${data_dir7}/bin/example-cli-1.0.0\"$" "${env_file}"; then
  pass "env_file_receives_derived_bin_env_export"
else
  fail "env_file_receives_derived_bin_env_export (contents: $(cat "${env_file}"))"
fi
rm -f "${env_file}"

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
