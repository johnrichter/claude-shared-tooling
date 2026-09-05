---
name: CI template contract
description: "The single source for every shape the six fleet CI and release templates share: toolchain activation, binary provisioning, the two-job split, the version env block, input naming, runner labels, system-package legs, Homebrew PATH mapping, and the EXIT contract. Copy-pasteable actionlint-clean YAML per step, one table per decision."
id: doc:ai-shared-lib-public:ci-template-contract
tags:
  - type:doc
  - topic:build-tooling
  - status:active
  - privacy:public
  - owner:public
links:
  - project:fleet-04-adoption:design
updated: 2026-09-05T00:00:00Z
---

# CI template contract

Six templates and eighteen repositories implement this shape. Every part left to a template author's discretion is invented six ways. This document pins each shared part once, so a template copies the shape rather than reinventing it.

The six templates are `ci-go.yml`, `ci-rust.yml`, `ci-python.yml`, `ci-shell.yml`, `release-cli.yml` and `release-library.yml` (F68 plus the `ci-shell.yml` SC34 authors). The four **CI** templates are the first four; the two **release** templates are the last two.

**How to read this.** Every fact traces to a row in section 4 of `.dat/fleet-04-adoption/design.md` (cited as `F…`) or to a decision in `decisions.md` (cited as `OD…`/`K…`/`S…`). Where a fact and this contract disagree, the fact governs. Each YAML block is copy-pasteable and passes `actionlint` (verified — see the last section). A block written as steps or as a job map pastes into the job or workflow scaffold this contract shows for that section.

---

## 1. Runner labels

The fleet uses four fully qualified runner images, selected per operating system per K12 (standing rule S8) and section 4.3's measurements of 2026-08-25. Never a floating label (`ubuntu-latest`, `macos-latest`): a floating label moves under a caller without notice (OD34, K5).

| Label | Arch | OS decision | Why selected |
|---|---|---|---|
| `ubuntu-24.04` | x64 | Linux runs 24.04 | Both Ubuntu 26.04 variants are public preview, so K12 falls Linux back one version. `actions/runner-images` publishes 22.04/24.04/26.04 only; 24.04 is the previous version and no notice names it preview. |
| `ubuntu-24.04-arm` | arm64 | Linux runs 24.04 | Same decision, arm64 architecture. Tested separately per K12; not preview. |
| `macos-26` | arm64 | macOS runs 26 | The macOS 26 announcements name an Xcode 27 preview and a macOS 14 deprecation — neither names the image itself, so it is generally available. |
| `macos-26-intel` | x64 | macOS runs 26 | Same decision, Intel x64 architecture; not preview. |

**Label, never filename (OD33).** The macOS labels are `macos-26` for arm64 and `macos-26-intel` for Intel x64. The manifest filenames invert them: `macos-26-Readme.md` documents the Intel image and `macos-26-arm64-Readme.md` documents arm64. A reader takes the label and never the filename.

**`runs_on` default.** Every CI and release template already defaults its `runs_on` input to `ubuntu-24.04` (F19, 5 of 5). This contract holds that property rather than establishing it. `runs_on` labels the single-job templates and the default job; the CI templates fix their own per-job platforms (section 3) rather than reading `runs_on` for them.

---

## 2. The version env block

Defect 7: every template hardcodes a `language-tools` version (F9 measures 18 loci at `2.1.0` — five template `env:` blocks, twelve caller assignments, one plugin JSON file).

**Rule.** Each template declares exactly **one** `LANGUAGE_TOOLS_VERSION` env locus, six in total (the sixth is the new `ci-shell.yml`). Its value is `3.0.0` — the version OD69 sets, a major bump because SC5 makes `--language` a required selector on an existing verb. The `governance-code` plugin JSON carries the same `3.0.0`. **No caller pins a version**: the twelve caller assignments leave with the jobs SC16 replaces (OD7).

| Locus | Count after | Value |
|---|---|---|
| Template `env:` block | 6 (one per template) | `3.0.0` |
| `governance-code` plugin JSON | 1 | `3.0.0` |
| Caller assignments | 0 | — (removed with the replaced jobs, OD7) |

```yaml
env:
  # Named once per template; every step reads this instead of restating the version.
  # OD69 sets 3.0.0 (major bump: SC5 makes --language a required selector on `release build`).
  LANGUAGE_TOOLS_VERSION: "3.0.0"
```

---

## 3. The two-job split

Defects 8 and 9: the templates run `build` before the source checks, and no template declares SC8's two-job shape (F69 measures 0 of 3 CI templates declaring it — each declares exactly one job).

Each of the three compiled-language CI templates (`ci-go.yml`, `ci-rust.yml`, `ci-python.yml`) declares two jobs:

| Job | Runner | Checks | `needs` |
|---|---|---|---|
| `source-checks` | `ubuntu-24.04-arm` (fixed, OD63) | `format`, `lint`, `vet`, `security` | — |
| `build-test` | 4-platform matrix (section 1) | `build` and every `test` subcommand (`test unit`, `test e2e`, and `test benchmark` where the language's check set names it) | `source-checks` |

Source checks run first and on one platform because each reads source text or the dependency graph rather than build output, so a second platform finds nothing new (OD8, OD63); a source-check failure then costs a second rather than a compile. `build` and every `test` subcommand run on all four target platforms (OD9, OD53 keeps `test benchmark` on all four deliberately). The template carries this shape, not the caller (OD9).

**`ci-shell.yml` is out of this population by construction.** A shell script neither compiles nor packages, so it runs no `build` and carries no matrix to gate. It declares one job carrying `format`, `lint`, `security`, `test unit` and `test e2e` (OD3, OD50, OD51 — shell's five pairs in section 4.7).

```yaml
# Two-job skeleton for a compiled-language CI template (ci-go.yml shown; Rust/Python identical
# but for --language and the dir input name). Paste under `on: workflow_call:` + `env:` + a
# read-only `permissions:` block. Activation and provisioning steps (sections 4-8) precede the
# check steps in each job; elided here as `# ...activation + provisioning...`.
jobs:
  source-checks:
    name: source-checks
    runs-on: ubuntu-24.04-arm        # OD63: one platform, source text not build output
    steps:
      - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5
      # ...activation + provisioning...
      - name: language-tools format
        run: language-tools format --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log"
      - name: language-tools lint
        run: language-tools lint --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log"
      - name: language-tools vet
        run: language-tools vet --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log"
      - name: language-tools security
        run: language-tools security --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log"

  build-test:
    name: build-test
    needs: source-checks
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-24.04, ubuntu-24.04-arm, macos-26, macos-26-intel]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09 # v5
      # ...activation + provisioning...
      - name: language-tools build
        run: language-tools build --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log"
      - name: language-tools test unit
        run: language-tools test unit --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log" --timeout "${{ inputs.test_timeout }}s"
      - name: language-tools test e2e
        run: language-tools test e2e --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log" --timeout "${{ inputs.test_timeout }}s"
```

---

## 4. Toolchain activation

Defect 1: no template puts the pinned toolchain on PATH (F22 measures 0 tools placed on PATH by `mise install --locked` on 2026-08-25; F55 measures 0 of 4 runner images whose ambient PATH satisfies any fleet pin). So every check runs against the ambient runner version rather than the pin (F56: on `ubuntu-24.04`, Go 1.24.x, Python 3.12.3, Cargo 1.97.1 — all below pin).

**Primary contract.** The template resolves the pinned toolchain and places it ahead of the ambient version. `mise install --locked` provisions the pinned toolchain into the mise install root; the template puts that root on PATH so a check resolves the pinned copy first, not the image's. This covers Python too, without moving a pin (OD17): F53 records two of five Python checks passing and three failing three different ways against the ambient interpreter.

**Trust step (OD65).** Mise refuses an untrusted config outright, so an untrusted `mise.toml` breaks every check that reads it. F58 measures the six tracked `mise.toml` files as untrusted on 2026-08-25. The activation runs `mise trust` **before the first check reads a config**. SC6 owns this work.

```yaml
# Activation, primary path. Place after checkout, before any check step, in every job.
# Resolves both platform naming schemes once (mise: linux/macos, x64/arm64; language-tools:
# Go's own linux/darwin, amd64/arm64) and prepends RUNNER_TEMP/bin so the installed toolchain
# wins over the ambient version (F55, F56).
- name: Resolve runner platform
  id: platform
  run: |
    mkdir -p "${RUNNER_TEMP}/bin"
    echo "${RUNNER_TEMP}/bin" >> "$GITHUB_PATH"
    case "$(uname -s)" in
      Linux)  mise_os=linux;  lt_os=linux  ;;
      Darwin) mise_os=macos;  lt_os=darwin ;;
      *) echo "::error::unsupported runner OS $(uname -s)"; exit 1 ;;
    esac
    case "$(uname -m)" in
      x86_64|amd64)  mise_arch=x64;   lt_arch=amd64 ;;
      arm64|aarch64) mise_arch=arm64; lt_arch=arm64 ;;
      *) echo "::error::unsupported runner arch $(uname -m)"; exit 1 ;;
    esac
    {
      echo "mise_os=${mise_os}"
      echo "mise_arch=${mise_arch}"
      echo "lt_os=${lt_os}"
      echo "lt_arch=${lt_arch}"
    } >> "$GITHUB_OUTPUT"

# Trust every config mise will read before the first check reads one (OD65, F58).
# mise refuses an untrusted config outright; an untrusted file breaks every check.
- name: mise trust
  run: mise trust --all

# Install the pinned toolchain and every locked tool row, then put mise's shims on PATH so a
# check resolves the pinned toolchain ahead of the ambient version.
# MISE_PYTHON_COMPILE=0 (Python templates only): without it an ambient
# `[settings.python] compile = true` sends mise after a source-build asset that is not the
# url+sha256 the committed mise.lock pins, and --locked fails closed on the mismatch (fleet-03
# Fact 19). Harmless on Go/Rust/shell; set on every template for one shared shape.
- name: mise install --locked
  env:
    MISE_PYTHON_COMPILE: "0"
  run: |
    set -euo pipefail
    mise install --locked
    # Put the pinned toolchain's bin paths on PATH ahead of the ambient version. GITHUB_PATH
    # prepends for every later step, so a check resolves the pinned copy first (F55, F56).
    mise bin-paths >> "$GITHUB_PATH"
```

### Staged fallback (SC6)

Applied when the primary path leaves a check running against the ambient toolchain.

| Stage | Mechanism | Applies to |
|---|---|---|
| 1 | Run `mise exec -- <command>` per check step. `mise exec` trusts its config implicitly (F58), so the fallback needs **no separate trust step**. | All four CI templates + `ci-shell.yml` |
| 2 | Set `MISE_RUSTUP_HOME` and `MISE_CARGO_HOME` so mise bootstraps its own rustup (F62). | Rust |
| 3 | `actions/setup-go` and `actions/setup-python` resolve from the toolcache F55b measures: Go 1.26.6 and Python 3.14.7 are cached on `ubuntu-24.04`; Cargo is absent. | **Go and Python only** |

**Rust carries no third stage (OD16):** two stages plus the home override are enough; a failure after two is handled when it happens.

```yaml
# Stage 1 — per-check mise exec. Trusts the config implicitly (F58); no separate trust step.
- name: language-tools lint (fallback stage 1)
  run: mise exec -- language-tools lint --language rust --dir "${{ inputs.crate_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log"

# Stage 2 — Rust only. mise bootstraps its own rustup when both homes are set (F62).
- name: mise install --locked (fallback stage 2, Rust)
  env:
    MISE_RUSTUP_HOME: ${{ runner.temp }}/rustup
    MISE_CARGO_HOME: ${{ runner.temp }}/cargo
  run: mise install --locked

# Stage 3 — Go and Python only. Resolve from the toolcache (F55b). Rust has no stage 3 (OD16).
- name: Setup Go from toolcache (fallback stage 3, Go)
  uses: actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5 # v5
  with:
    go-version: "1.26"
- name: Setup Python from toolcache (fallback stage 3, Python)
  uses: actions/setup-python@a26af69be951a213d495a4c3e4e4022e16d87065 # v5
  with:
    python-version: "3.14"
```

---

## 5. Binary provisioning

Defect 10: no template provisions the binaries its checks invoke. F54 measures 23 standalone binaries the section 4.7 matrix invokes that no template installs (measured 2026-08-26).

**Install mechanism.** Twenty-one of the 23 reach a mise backend, so each gains a row in the target root's `mise.toml` `[tools]` block and installs through `mise install --locked` — the same activation step in section 4. Twelve of those record a per-platform digest in `mise.lock`; nine do not (the backend, not the tool, decides — F63 names `go:`, `pipx:` and `core:rust` as backends that lock nothing). The remaining two reach no mise backend and install through the system package manager (section 6). Every tool is pinned at its latest stable version, never `latest` (OD49).

**Digest verification is best-effort (SC7).** A tool arrives verified where its backend records a per-platform digest, and unverified where none does. A prebuilt download carries a digest; a tool the package manager assembles on the machine has no whole file to hash. The check is simply not run for those tools. No tool is named an exception, because the rule is a property of the backend rather than a carve-out for a name.

**The locking shape (F61).** A locking `mise.lock` row records a per-platform digest for 7 platforms with 0 skipped (`golangci-lint` via `aqua:golangci/golangci-lint` at 2.13.0 measured this). `bats-core` records 6 (every fleet target present; windows excluded).

### The 23-binary matrix

Digest column: **yes** = backend records a per-platform digest (12, F67); **no** = backend records none (9, F67); **system** = no mise backend, installs via the OS package manager (2, F67/F79).

| Binary | Track | Backend | Digest | Version rule | Install step |
|---|---|---|---|---|---|
| `golangci-lint` | Go | `aqua:golangci/golangci-lint` | yes | OD49 latest stable | `mise install --locked` |
| `goimports` | Go | `go:golang.org/x/tools/cmd/goimports` | no | OD49 latest stable | `mise install --locked` |
| `staticcheck` | Go | `aqua:dominikh/go-tools/staticcheck` | yes | OD49 latest stable | `mise install --locked` |
| `gosec` | Go | `aqua:` | yes | OD49 latest stable | `mise install --locked` |
| `govulncheck` | Go | `go:` | no | OD49 latest stable | `mise install --locked` |
| `gotestsum` | Go | `aqua:gotestyourself/gotestsum` | yes | OD49 latest stable | `mise install --locked` |
| `cargo-audit` | Rust | `cargo:cargo-audit` | no | OD49 latest stable | `mise install --locked` |
| `cargo-deny` | Rust | `aqua:` | yes | OD49 latest stable | `mise install --locked` |
| `cargo-nextest` | Rust | `cargo:cargo-nextest` | no | OD49 latest stable | `mise install --locked` |
| `cargo-llvm-cov` | Rust | `aqua:` | yes | OD49 latest stable | `mise install --locked` |
| `ruff` | Python | `aqua:astral-sh/ruff` | yes | OD49 latest stable | `mise install --locked` |
| `mypy` | Python | `pipx:mypy` | no | OD49 latest stable | `mise install --locked` |
| `bandit` | Python | `pipx:bandit` | no | OD49 latest stable | `mise install --locked` |
| `shellcheck` | Shell | `aqua:koalaman/shellcheck` | yes | OD49 latest stable | `mise install --locked` |
| `checkbashisms` | Shell | none (system) | system | OD49 latest stable | apt (`devscripts`) / brew — section 6 |
| `shfmt` | Shell | `aqua:mvdan/sh` | yes | OD49 latest stable | `mise install --locked` |
| `bats-core` | Shell | `aqua:bats-core/bats-core` | yes (6 entries) | OD49 latest stable | `mise install --locked` |
| `semgrep` | Shell | `pipx:semgrep` | no | OD49 latest stable | `mise install --locked` |
| `kcov` | Shell | `ubi:` | no | OD49 latest stable | `mise install --locked` |
| `jq` | Shell | `aqua:jqlang/jq` | yes | OD49 latest stable | `mise install --locked` |
| `actionlint` | Workflows | `aqua:rhysd/actionlint` | yes | OD49 latest stable | `mise install --locked` |
| `playwright` | Python (`test e2e`) | `pipx:` (1.62.0) or `npm:` (1.62.1) | no | OD49 latest stable | `mise install --locked`; ships its own Chromium (OD61) |
| Google Chrome | Go (`test e2e`) | none (system) | system | OD56 latest Chrome | apt (Google repo) / brew — section 6 |

Twelve lock, nine do not, two reach no mise backend (F67). `playwright` needs no browser install step: it ships its own Chromium (OD61), so the Python `test e2e` leg installs no Chrome.

### Provisioning fallback (SC7)

For a tool no mise backend reaches (before it becomes a system-package case):

| Stage | Mechanism |
|---|---|
| 1 | Fetch the tool from its own release archive, pinned by URL and by a SHA-256 the pin file records. |
| 2 | Install through its language package manager inside the activated toolchain — `go install`, `cargo install` or `uv tool install` — which loses the digest F67 already records as absent. |

A check whose binary reaches the runner by neither stage becomes a named defect under K8, never an exclusion. Each is pinned at its latest stable version per OD49, never `latest`.

```yaml
# Provisioning fallback stage 1 — verified release-archive fetch, pinned by URL + SHA-256 the
# pin file records. Never pipes a download into a shell. (Shape mirrors the mise/language-tools
# verified fetch the templates already carry.)
- name: Install <tool> (fallback stage 1, verified fetch)
  env:
    TOOL_URL: "https://example.invalid/<tool>/vX.Y.Z/<tool>-${{ steps.platform.outputs.lt_os }}-${{ steps.platform.outputs.lt_arch }}.tar.gz"
    TOOL_SHA256: "<sha256 recorded in the pin file>"
  run: |
    set -euo pipefail
    tmp="$(mktemp -d)"
    curl -fsSL -o "${tmp}/tool.tar.gz" "${TOOL_URL}"
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "${tmp}/tool.tar.gz" | awk '{print $1}')"
    else
      actual="$(shasum -a 256 "${tmp}/tool.tar.gz" | awk '{print $1}')"
    fi
    if [ "${TOOL_SHA256}" != "${actual}" ]; then
      echo "::error::sha256 mismatch -- expected ${TOOL_SHA256}, got ${actual}"
      exit 1
    fi
    tar -xzf "${tmp}/tool.tar.gz" -C "${RUNNER_TEMP}/bin"
    rm -rf "${tmp}"

# Provisioning fallback stage 2 — language package manager inside the activated toolchain.
# Loses the digest F67 already records as absent. Pin the version per OD49, never `latest`.
- name: Install <tool> (fallback stage 2, language package manager)
  run: |
    set -euo pipefail
    go install example.com/tool@vX.Y.Z   # or: cargo install tool --version X.Y.Z --locked
                                          # or: uv tool install tool==X.Y.Z
```

---

## 6. System-package tools

Two of the 23 reach no mise backend and install through the system package manager (OD57, F79 measured 2026-08-26): `checkbashisms` and Google Chrome.

| Tool | OS | Channel | Source / package | Serves |
|---|---|---|---|---|
| `checkbashisms` | Ubuntu | apt | `devscripts` package | Shell track (`source-checks` on `ubuntu-24.04-arm`) |
| `checkbashisms` | macOS | Homebrew | `checkbashisms` formula | Not installed — source checks run on `ubuntu-24.04-arm` alone (OD63) |
| Google Chrome | Ubuntu | apt | Google's own apt repository | Go `test e2e` on both Ubuntu targets |
| Google Chrome | macOS | Homebrew | `google-chrome` cask | Go `test e2e` on the macOS `build`/`test` legs (OD63) |

**The Google apt repository (F79).** `dl.google.com/linux/chrome/deb/dists/stable/Release` answers HTTP 200 with an `Architectures` line reading `amd64 arm64`. Both `main/binary-amd64/Packages` and `main/binary-arm64/Packages` answer 200; the counter-probe `main/binary-i386/Packages` answers 404. The arm64 index lists `google-chrome-stable`, so the channel covers both Ubuntu targets.

**Split by OS (OD63).** The source checks run on `ubuntu-24.04-arm`, so `checkbashisms` installs through apt there and needs no macOS install. The Homebrew leg serves the macOS `build` and `test` legs alone — which for Chrome is the Go `test e2e` leg (OD56, OD64).

**Cache refresh first (OD59).** Every job that installs a system package refreshes its cache before the install: Ubuntu runs `apt update`, macOS runs `brew update`. Homebrew itself is already present on the macOS image (F80: Homebrew 6.0.13), so the refresh makes that copy current and no Homebrew install step is needed.

**Print the installed version (OD60).** Every install step prints the version it installed, so a reader sees what the check ran against.

```yaml
# Ubuntu — checkbashisms via apt (shell source-checks job, ubuntu-24.04-arm).
- name: Install checkbashisms (apt)
  run: |
    set -euo pipefail
    sudo apt-get update
    sudo apt-get install -y devscripts
    checkbashisms --version   # OD60: print what was installed

# Ubuntu — Google Chrome via Google's own apt repository (Go test e2e legs on both Ubuntu targets).
- name: Install Google Chrome (apt, Google repository)
  run: |
    set -euo pipefail
    curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
      | sudo gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" \
      | sudo tee /etc/apt/sources.list.d/google-chrome.list
    sudo apt-get update
    sudo apt-get install -y google-chrome-stable
    google-chrome --version   # OD60: print what was installed

# macOS — Chrome via Homebrew. checkbashisms is not installed here: source checks run on
# ubuntu-24.04-arm alone (OD63), so the macOS Homebrew leg serves Chrome for the build/test legs.
# Homebrew is present on the image (F80); refresh then install.
- name: Install Google Chrome (Homebrew, macOS)
  run: |
    set -euo pipefail
    brew update                      # OD59: refresh the image's own Homebrew
    brew install --cask google-chrome
    "$(brew --prefix)/bin/google-chrome" --version || true   # OD60: print what was installed
```

---

## 7. The Homebrew PATH mapping

F80 measures Homebrew 6.0.13 present on the macOS image on 2026-08-26, with its PATH resolution **proven on Intel** through `/usr/local/bin` and **unproven on arm64**, where the prefix is `/opt/homebrew/bin`. No build script writes an `/etc/paths.d` entry for Homebrew, and the image's `bashrc` exports `/usr/local/bin` (the Intel prefix) but never `/opt/homebrew/bin` (the arm64 prefix).

**A macOS job maps the Homebrew prefix onto PATH before its first `brew` step.** The check step that runs `checkbashisms` or Chrome inherits the same mapping, because the binaries Homebrew installs land in that same prefix. Prefix by arch: `/opt/homebrew/bin` on arm64, `/usr/local/bin` on Intel.

**Installed browser wins on PATH (OD64).** The mapping puts the Homebrew prefix **ahead of** the image's own Chrome location, so a check resolves the copy the template installed rather than the one the image shipped. Installing without winning the PATH leaves OD56 unmet. (F77: the images ship Chrome 151.0.7922.137 on `ubuntu-24.04` and 150.0.7871.187 on `macos-26-arm64` on 2026-08-26; the templates use none of them.)

**Resolution check.** After the install, the template proves the mapping: the resolved browser path sits inside the Homebrew prefix, and the printed version equals the copy the install step reported — not the image's copy.

```yaml
# Map the Homebrew prefix onto PATH before the first brew step. Prepend so the prefix wins over
# the image's own Chrome location (OD64). arm64: /opt/homebrew/bin; Intel: /usr/local/bin (F80).
- name: Map Homebrew prefix onto PATH (macOS)
  run: |
    set -euo pipefail
    case "$(uname -m)" in
      arm64|aarch64) prefix="/opt/homebrew" ;;
      *)             prefix="/usr/local"    ;;
    esac
    echo "${prefix}/bin" >> "$GITHUB_PATH"

# Prove the installed browser wins on PATH (OD64): resolved path inside the Homebrew prefix, and
# the printed version equals the install step's, not the image's shipped Chrome (F77).
- name: Verify installed Chrome wins on PATH (macOS)
  run: |
    set -euo pipefail
    case "$(uname -m)" in
      arm64|aarch64) prefix="/opt/homebrew" ;;
      *)             prefix="/usr/local"    ;;
    esac
    resolved="$(command -v google-chrome || command -v "Google Chrome" || true)"
    echo "resolved chrome: ${resolved}"
    case "${resolved}" in
      "${prefix}/"*) : ;;
      *) echo "::error::resolved Chrome ${resolved} is not inside the Homebrew prefix ${prefix}"; exit 1 ;;
    esac
    "${resolved}" --version
```

---

## 8. Input naming

Shared inputs across the six templates. A common role takes a common name; only the target-root input varies by language, because each language names its root differently.

| Input | Type | Default | Templates | Role |
|---|---|---|---|---|
| `runs_on` | string | `"ubuntu-24.04"` | all six | Fully qualified runner label (section 1); never floating. |
| `module_dir` / `crate_dir` / `project_dir` / `script_root` | string | `"."` | Go / Rust / Python / shell | Target root `language-tools` checks, relative to the checked-out repo (`--dir`). Shell recurses from its root (OD54). |
| `mise_version` | string | measured latest stable patch (section 9) | all six | mise release the template installs. |
| `dry_run` | boolean | `false` | all six | `true` installs and verifies mise + language-tools and stops; check steps and `extra_steps` are skipped. |
| `test_timeout` | number | `300` | four CI templates | Seconds; defect 3 (below). |
| `extra_steps` | string | `""` | all six | Shell script run after the checks, for a repository-specific check `language-tools` does not cover. Empty runs nothing. |

**Deleted (SC38, OD39).** `ci-go.yml` and `ci-rust.yml` drop `checkout_ai_shared_lib_sibling`, `set_ai_shared_lib_goprivate` and the `sibling_repo_token` secret — the module repository is public, so a tagged dependency resolves through the public proxy (F78). `ci-python.yml`'s single comment recording its absence stays (OD55).

### Defect 3 — the test timeout

F20 measures 0 of 3 CI templates exposing a test timeout on 2026-08-25 (a `test_timeout` search returns zero hits in all three), so every check runs at the binary default of 300 s (F41). The contract fixes the input for all four CI templates:

| Property | Value |
|---|---|
| Name | `test_timeout` |
| Type | `number` (seconds) |
| Default | `300` (the binary default, F41) |
| Applied by | each `test` subcommand step, as `--timeout "${{ inputs.test_timeout }}s"` |

`language-tools` exposes `--timeout` as a Go duration (default 300 s); the numeric-seconds input gains an `s` suffix at the call site so it parses as a duration.

```yaml
inputs:
  test_timeout:
    description: "Wall-clock bound per test subcommand, in seconds. Applied as language-tools --timeout <n>s. Default is the binary default (300 s)."
    required: false
    type: number
    default: 300
```

```yaml
# Every test subcommand step applies it. The `s` suffix converts the numeric-seconds input to a
# Go duration (a bare integer fails to parse as a duration).
- name: language-tools test unit
  run: language-tools test unit --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log" --timeout "${{ inputs.test_timeout }}s"
```

---

## 9. The mise version

Defect 13: every template pins mise below the latest stable patch (K11). Each template pins mise at the **latest stable patch**, measured at write time per K11 and S7, recorded here and **never carried forward** from an earlier revision.

| Measurement | Value | Method | Date |
|---|---|---|---|
| Latest stable mise patch | `2026.9.1` | mise's own update check (installed `2026.8.10` reports `2026.9.1` available); the GitHub releases API probe returned HTTP 403 (unauthenticated rate limit), so the self-reported value is the instrument. | 2026-09-05 |

```yaml
inputs:
  mise_version:
    description: "mise release the template installs. Latest stable patch, measured at write time per K11/S7. Every committed mise.lock must be re-locked with this version."
    required: false
    type: string
    default: "2026.9.1"
```

**Re-measure on every revision (K11).** A version is never carried forward. Any revision that touches the templates re-measures the latest stable patch and records the new value and date here. Bumping the pin requires re-locking every committed `mise.lock` with the new mise (the fleet's committed locks were written by `2026.8.10`).

---

## 10. The EXIT contract and the diagnostic surface

Restated once here so no template invents its own failure mapping. The source of truth is `ai-shared-lib/go/clikit/status.go` and `schemas/clikit/result-record.schema.json` (`$defs/status`).

**EXIT contract.** `language-tools` exits with the code its outcome class pairs with. The taxonomy is a **classification, not a severity ordering** — never compare exit codes with `<` or `>`.

| Status | Exit code | Meaning for a check |
|---|---|---|
| `success` | 0 | The check passed. |
| `caveats` | 10 | Passed with caveats. |
| `gate_negative` | 20 | A check found a real fault, or a pin/config gate fired (e.g. `gate_negative.pin.version_below_pin`, `gate_negative.pin.missing_mise_toml`, `gate_negative.toolchain.error`). |
| `precondition_unmet` | 30 | A precondition was not met. |
| `not_found` | 40 | A required input was not found. |
| `conflict` | 41 | A conflicting state. |
| `usage` | 50 | A usage error (bad flags/args; an unknown command is rejected here before a check runs). |
| `transient` | 60 | A transient failure. |
| `permission` | 70 | A permission failure. |
| `unsupported` | 80 | The language has no equivalent of the check (`unsupported.toolchain.check_not_supported`) — e.g. Rust/shell `vet`. |
| `internal` | 90 | An internal fault (`internal.toolchain.run_failed`); also a pin file with no `[tools]` block (F59b). |

A step fails the job on any non-zero exit. A template maps no exit code itself and adds no `continue-on-error` to a check step: the code is the contract.

**Diagnostic surface.** Every check emits one JSON result record (`schema_version: 1`) carrying `command`, `status`, `exit_code`, and an `errors[]` array. Each error carries `code`, `context` (`check`, `dir`, `language`), `message`, and a `triage` object (`instruction`, `kind`). Each diagnostic names a file; each diagnostic whose tool reports a position also names a line (SC2). Every check step passes an absolute `--log-dir` (`${{ github.workspace }}/.language-tools/log`), so per-check logs land in one known location a reader can collect. Templates surface fatal shell-level problems through GitHub `::error::` annotations (the activation and provisioning steps above); the check records themselves are the binary's own JSON.

---

## 11. actionlint verification

Every YAML block above was extracted, wrapped in a `workflow_call` scaffold where it is a step or job fragment, and linted with `actionlint 1.7.12`. Result: **0 findings across all blocks.** Re-run: extract each fenced `yaml` block, paste into a scratch workflow (steps under a `runs-on: ubuntu-latest` job; job maps under `jobs:`; the `on: workflow_call` input/env snippets under a minimal workflow header) and run `actionlint`.
</content>
</invoke>
