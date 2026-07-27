#!/usr/bin/env sh
# download-script.sh -- generic per-OS/arch binary provisioner shared by every
# govern-now CLI's plugin. A plugin never copies this file; its SessionStart
# hook execs it with the env below set to that plugin's own CLI name, data
# dir, and release host.
#
# Ladder: (1) a cached binary for the pinned version that still matches its
# own recorded sha256 -- no network; (2) download the per-arch artifact +
# its .sha256 sidecar from the release host, verify, cache. Anything short
# of a verified binary is a soft failure (no crash): the caller (typically
# forced-use-hook.sh, indirectly, via CLI-availability probing) treats an
# unresolved binary the same as a CLI that was never installed and fails
# open to the raw OS tool.
#
# Inputs (env):
#   PF_CLI_NAME          required -- the governed CLI's name. Used to build
#                        the cached binary's filename and the artifact URL,
#                        and (unless PF_BIN_ENV overrides it) the exported
#                        env var name: PF_CLI_NAME with '-' -> '_', upper-
#                        cased, suffixed "_BIN".
#   PF_PLUGIN_DATA       required -- persistent per-plugin data directory
#                        (Claude Code's CLAUDE_PLUGIN_DATA). The version-
#                        keyed binary cache lives at "$PF_PLUGIN_DATA/bin".
#   PF_RELEASE_BASE_URL  required -- release host root. A file:// URL for
#                        tests and air-gapped mirrors, https:// otherwise.
#   PF_VERSION           the pinned version string. Takes precedence over
#                        PF_VERSION_FILE when both are set.
#   PF_VERSION_FILE      path to a plugin.json-shaped file; the pinned
#                        version is read from its top-level "version" field.
#                        Required when PF_VERSION is unset.
#   PF_BIN_ENV           env var name the verified binary path is exported
#                        under. Default: derived from PF_CLI_NAME (above).
#   PF_ENV_FILE          file to append `export $PF_BIN_ENV=...` to
#                        (Claude Code's CLAUDE_ENV_FILE). Unset: skip export,
#                        the resolved path still prints to stdout.
#   PF_ARCH_OVERRIDE     test-only: skip uname resolution, use this arch id.
#
# Release layout expected under PF_RELEASE_BASE_URL:
#   <name>-v<version>/<name>-<version>-<arch>          the binary artifact
#   <name>-v<version>/<name>-<version>-<arch>.sha256    its sha256 (accepts
#                                                        either a bare hex
#                                                        digest or a
#                                                        `sha256sum`-style
#                                                        "<hash>  <name>" line)
#
# Outputs:
#   "$PF_PLUGIN_DATA/bin/<name>-<version>"          the verified binary
#   "$PF_PLUGIN_DATA/bin/<name>-<version>.sha256"   its verified digest
#   stdout: the verified binary's absolute path (success only)
#   $PF_ENV_FILE: `export $PF_BIN_ENV=<path>` (success only, when set)
#
# Exit codes: 0 verified (path on stdout); 1 no verified binary produced
# (unreachable host, sha256 mismatch, unsupported arch, unresolved version --
# a soft failure, never a crash); 2 misconfigured (a required env var is
# unset) -- distinct from 1 because it is a plugin wiring bug, not a runtime
# provisioning outcome.
set -eu

warn() {
  echo "download-script: $*" >&2
}

require_env() {
  eval "val=\${$1:-}"
  if [ -z "${val}" ]; then
    warn "required env var $1 is not set"
    exit 2
  fi
}

sha256_of() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    return 1
  fi
}

# sha256_sidecar_value -- prints the hex digest a sidecar file names, whether
# it holds a bare digest or a `sha256sum`-style "<hash>  <filename>" line.
sha256_sidecar_value() {
  awk '{print $1; exit}' "$1" 2>/dev/null
}

resolve_arch() {
  if [ -n "${PF_ARCH_OVERRIDE:-}" ]; then
    echo "${PF_ARCH_OVERRIDE}"
    return
  fi
  kernel="$(uname -s)"
  machine="$(uname -m)"
  case "${kernel}" in
  Linux) os="linux" ;;
  Darwin) os="macos" ;;
  *)
    warn "unsupported kernel '${kernel}'"
    return 1
    ;;
  esac
  case "${machine}" in
  x86_64 | amd64) arch="x86_64" ;;
  arm64 | aarch64) arch="aarch64" ;;
  *)
    warn "unsupported machine type '${machine}'"
    return 1
    ;;
  esac
  echo "${os}-${arch}"
}

# read_version -- prints the pinned version: PF_VERSION verbatim, or the
# "version" field of PF_VERSION_FILE (jq when available, else a grep/sed
# fallback matching plugin.json's fixed one-field-per-line shape).
read_version() {
  if [ -n "${PF_VERSION:-}" ]; then
    printf '%s' "${PF_VERSION}"
    return 0
  fi
  if [ -z "${PF_VERSION_FILE:-}" ]; then
    warn "neither PF_VERSION nor PF_VERSION_FILE is set"
    return 1
  fi
  if [ ! -f "${PF_VERSION_FILE}" ]; then
    warn "PF_VERSION_FILE not found: ${PF_VERSION_FILE}"
    return 1
  fi
  if command -v jq >/dev/null 2>&1; then
    jq -r '.version' "${PF_VERSION_FILE}"
    return
  fi
  grep -m1 '"version"' "${PF_VERSION_FILE}" | sed -E 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]*)".*/\1/'
}

# fetch SRC DEST -- src may be file:// (tests, air-gapped mirrors) or
# http(s)://, fetched with whichever of curl/wget is on PATH.
fetch() {
  case "$1" in
  file://*)
    cp "${1#file://}" "$2"
    ;;
  *)
    if command -v curl >/dev/null 2>&1; then
      curl -fsSL -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
      wget -q -O "$2" "$1"
    else
      warn "neither curl nor wget is available -- cannot fetch $1"
      return 1
    fi
    ;;
  esac
}

# default_bin_env NAME -- NAME with '-' -> '_', uppercased, suffixed "_BIN".
default_bin_env() {
  printf '%s' "$1" | tr '[:lower:]-' '[:upper:]_'
  printf '_BIN'
}

require_env PF_CLI_NAME
require_env PF_PLUGIN_DATA
require_env PF_RELEASE_BASE_URL

bin_env="${PF_BIN_ENV:-$(default_bin_env "${PF_CLI_NAME}")}"

version="$(read_version || true)"
if [ -z "${version}" ]; then
  warn "could not resolve a pinned version"
  exit 1
fi

bin_dir="${PF_PLUGIN_DATA}/bin"
bin_path="${bin_dir}/${PF_CLI_NAME}-${version}"
sha_sidecar="${bin_path}.sha256"
verified=0

# Idempotent cache fast path: bytes already on disk that still match their
# own recorded digest need no network round trip at all.
if [ -f "${bin_path}" ] && [ -f "${sha_sidecar}" ]; then
  recorded="$(sha256_sidecar_value "${sha_sidecar}" || true)"
  actual="$(sha256_of "${bin_path}" 2>/dev/null || true)"
  if [ -n "${recorded}" ] && [ "${recorded}" = "${actual}" ]; then
    verified=1
  else
    warn "cached ${bin_path} failed local re-verification -- re-downloading"
  fi
fi

if [ "${verified}" -eq 0 ]; then
  arch="$(resolve_arch || true)"
  if [ -z "${arch}" ]; then
    exit 1
  fi

  artifact_url="${PF_RELEASE_BASE_URL}/${PF_CLI_NAME}-v${version}/${PF_CLI_NAME}-${version}-${arch}"
  sha_url="${artifact_url}.sha256"

  mkdir -p "${bin_dir}"
  sha_tmp="$(mktemp "${bin_dir}/.sha256.XXXXXX")"
  artifact_tmp="$(mktemp "${bin_dir}/.download.XXXXXX")"

  if fetch "${sha_url}" "${sha_tmp}" && fetch "${artifact_url}" "${artifact_tmp}"; then
    expected="$(sha256_sidecar_value "${sha_tmp}" || true)"
    actual="$(sha256_of "${artifact_tmp}" 2>/dev/null || true)"
    if [ -n "${expected}" ] && [ "${expected}" = "${actual}" ]; then
      chmod +x "${artifact_tmp}"
      mv "${artifact_tmp}" "${bin_path}"
      echo "${actual}" >"${sha_sidecar}"
      verified=1
    else
      warn "sha256 mismatch for ${PF_CLI_NAME}-${version}-${arch} -- expected ${expected:-<none>}, got ${actual:-<none>}"
    fi
  else
    warn "failed to fetch ${artifact_url} (or its .sha256 sidecar)"
  fi
  rm -f "${sha_tmp}" "${artifact_tmp}"
fi

if [ "${verified}" -ne 1 ]; then
  warn "no verified binary for ${PF_CLI_NAME} ${version}"
  exit 1
fi

if [ -n "${PF_ENV_FILE:-}" ]; then
  echo "export ${bin_env}=\"${bin_path}\"" >>"${PF_ENV_FILE}"
fi
echo "${bin_path}"
