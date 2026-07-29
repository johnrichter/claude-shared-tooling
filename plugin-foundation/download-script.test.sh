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
readonly ARCHIVE="${RELEASE_FIXTURE}/v1.0.0/example-cli_1.0.0_linux_amd64.tar.gz"
readonly EXTRACTED_DIGEST="462cb18967f08c936f91df221ccba3b4d7f98ee83d03d1fb71ee749dc7f3a30d"

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

# Test 1: fresh cache -- fetches the archive, verifies its digest against
# checksums.txt, extracts the embedded binary, caches it, prints its path,
# exits 0.
data_dir="$(fresh_data_dir)"
out="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir}" \
    PF_RELEASE_BASE_URL="file://${RELEASE_FIXTURE}" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux/amd64 \
    "${SCRIPT}" 2>/tmp/download-script.test.stderr
)" && exit_code=0 || exit_code=$?
if [[ ${exit_code} -eq 0 && "${out}" == "${data_dir}/bin/example-cli-1.0.0" && -f "${out}" && -f "${out}.sha256" ]]; then
  pass "fresh_fetch_verifies_and_caches"
else
  fail "fresh_fetch_verifies_and_caches (exit=${exit_code}, out='${out}')"
fi

if [[ "$(cat "${data_dir}/bin/example-cli-1.0.0.sha256")" == "${EXTRACTED_DIGEST}" ]]; then
  pass "cached_binary_digest_matches_extracted_fixture_bytes"
else
  fail "cached_binary_digest_matches_extracted_fixture_bytes"
fi

# Test 2: idempotent cache -- second run against an unreachable release host
# still succeeds, because the fast path re-verifies the cached binary against
# its own recorded digest and never touches the network.
out2="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir}" \
    PF_RELEASE_BASE_URL="file:///nonexistent-release-host" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux/amd64 \
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
    PF_ARCH_OVERRIDE=linux/amd64 \
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
mkdir -p "${bad_fixture_dir}/v1.0.0"
cp "${ARCHIVE}" "${bad_fixture_dir}/v1.0.0/example-cli_1.0.0_linux_amd64.tar.gz"
echo "0000000000000000000000000000000000000000000000000000000000000000  example-cli_1.0.0_linux_amd64.tar.gz" >"${bad_fixture_dir}/v1.0.0/checksums.txt"
data_dir4="$(fresh_data_dir)"
out4="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir4}" \
    PF_RELEASE_BASE_URL="file://${bad_fixture_dir}" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux/amd64 \
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
    PF_ARCH_OVERRIDE=linux/amd64 \
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
  PF_ARCH_OVERRIDE=linux/amd64 \
  PF_ENV_FILE="${env_file}" \
  "${SCRIPT}" >/dev/null 2>/tmp/download-script.test.stderr
if grep -q "^export EXAMPLE_CLI_BIN=\"${data_dir7}/bin/example-cli-1.0.0\"$" "${env_file}"; then
  pass "env_file_receives_derived_bin_env_export"
else
  fail "env_file_receives_derived_bin_env_export (contents: $(cat "${env_file}"))"
fi
rm -f "${env_file}"

# Test 8: a release host that is reachable (checksums.txt fetches, and lists
# the archive) but never actually shipped the archive itself is a soft
# failure, not a crash -- exit 1, no stdout, nothing cached.
missing_archive_dir="$(mktemp -d)"
mkdir -p "${missing_archive_dir}/v1.0.0"
cp "${RELEASE_FIXTURE}/v1.0.0/checksums.txt" "${missing_archive_dir}/v1.0.0/checksums.txt"
data_dir8="$(fresh_data_dir)"
out8="$(
  PF_CLI_NAME=example-cli \
    PF_PLUGIN_DATA="${data_dir8}" \
    PF_RELEASE_BASE_URL="file://${missing_archive_dir}" \
    PF_VERSION=1.0.0 \
    PF_ARCH_OVERRIDE=linux/amd64 \
    "${SCRIPT}" 2>/tmp/download-script.test.stderr
)" && exit_code8=0 || exit_code8=$?
if [[ ${exit_code8} -eq 1 && -z "${out8}" && ! -f "${data_dir8}/bin/example-cli-1.0.0" ]]; then
  pass "missing_archive_with_checksums_present_is_soft_failure"
else
  fail "missing_archive_with_checksums_present_is_soft_failure (exit=${exit_code8}, out='${out8}')"
fi
rm -rf "${missing_archive_dir}"

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
