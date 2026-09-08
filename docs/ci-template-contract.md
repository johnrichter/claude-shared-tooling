---
name: CI template contract
description: "The single source for every shape the seven fleet CI and release templates share: toolchain activation, binary provisioning, the two-job split, the version env block, input naming, runner labels, system-package legs, Homebrew PATH mapping, the EXIT contract, and the workflow track's exit-to-annotation override. Copy-pasteable actionlint-clean YAML per step, one table per decision."
id: doc:ai-shared-lib-public:ci-template-contract
tags:
  - type:doc
  - topic:build-tooling
  - status:active
  - privacy:public
  - owner:public
links:
  - project:fleet-04-adoption:design
updated: 2026-09-07T00:00:00Z
---

# CI template contract

Seven templates and eighteen repositories implement this shape. Every part left to a template author's discretion is invented six ways. This document pins each shared part once, so a template copies the shape rather than reinventing it.

The seven templates are `ci-go.yml`, `ci-rust.yml`, `ci-python.yml`, `ci-shell.yml`, `ci-workflow.yml`, `release-cli.yml` and `release-library.yml` (F68 plus the `ci-shell.yml` SC34 and `ci-workflow.yml` SC39 authors). The five **CI** templates are the first five; the two **release** templates are the last two.

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

`ci-shell.yml` and `ci-workflow.yml` both default `runs_on` to `"ubuntu-24.04-arm"` instead, naming their own fixed job platform (OD63) rather than F19's default; both sit outside F19's population.

---

## 2. The version env block

Defect 7: every template hardcodes a `language-tools` version (F9 measures 18 loci at `2.1.0` — five template `env:` blocks, twelve caller assignments, one plugin JSON file).

**Rule.** Each template declares exactly **one** `LANGUAGE_TOOLS_VERSION` env locus, seven in total (the sixth was `ci-shell.yml`; the seventh is the new `ci-workflow.yml`). Its value is `3.0.2` — a patch bump over `3.0.1` (both patch the `3.0.0` OD69 first set, whose major bump made `--language` a required selector on an existing verb, SC5). `3.0.2` carries two `go/toolchain` fixes the binary reaches CI with only through a module retag: the rust unit/e2e test-filterset correction (a `binary()` name matcher that matched no binary failed every crate before a test ran) and the in-process failure-path output capture. The `governance-code` plugin JSON must carry the same `3.0.2`. **No caller pins a version**: the twelve caller assignments leave with the jobs SC16 replaces (OD7).

| Locus | Count after | Value |
|---|---|---|
| Template `env:` block | 7 (one per template) | `3.0.2` |
| `governance-code` plugin JSON | 1 | `3.0.2` |
| Caller assignments | 0 | — (removed with the replaced jobs, OD7) |

```yaml
env:
  # Named once per template; every step reads this instead of restating the version.
  # 3.0.2 patch-bumps 3.0.1 (both patch the 3.0.0 OD69 first set, whose major
  # bump made --language a required selector on `release build`, SC5); it carries
  # the go/toolchain rust test-filterset fix and the in-process failure-path
  # capture fix.
  LANGUAGE_TOOLS_VERSION: "3.0.2"
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

**`ci-workflow.yml` is out of this population too.** WORKFLOW-PAIR (OD71) carves the workflow track out of the section 4.7 language matrix rather than adding it as a column, so the track owns one pair — `workflow lint` — and no `build`. It declares one job for the same reason `ci-shell.yml` does: the check reads source text, not build output, so a second platform or a build-then-test split would find nothing new.

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
      - name: language-tools test unit        # unit pair anchors --dir absolute; see note below
        run: language-tools test unit --language go --dir "${{ github.workspace }}/${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log" --timeout "${{ inputs.test_timeout }}s"
      - name: language-tools test e2e
        run: language-tools test e2e --language go --dir "${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log" --timeout "${{ inputs.test_timeout }}s"
```

**The unit pair anchors `--dir` absolute.** `language-tools test unit` wraps `go test` in gotestsum and writes `junit.xml` and `coverage.out` at `<dir>/<name>`, while the check already runs with its working directory set to `<dir>`. A relative `--dir` (e.g. `go/agentcontract`) therefore doubles into `<dir>/<dir>/<name>`, whose parent does not exist, and gotestsum exits 1 in ~30 ms having run zero tests. `ci-go.yml` passes this one step `--dir "${{ github.workspace }}/${{ inputs.module_dir }}"` so the write path resolves absolute against the real directory. The target root and subject set are unchanged — only the path is anchored. `build` and `test e2e` write no `<dir>`-relative output and keep the plain relative `--dir`. The dir-relative write is a `language-tools` behavior, not the template's; the anchoring is the template-side remedy the pinned binary needs, and the rust unit pair (which writes `lcov.info` the same way) carries the same latent shape behind its own source-checks gate.

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
| 1 | Run `mise exec -- <command>` per check step. `mise exec` trusts its config implicitly (F58), so the fallback needs **no separate trust step**. | All four CI templates + `ci-shell.yml` + `ci-workflow.yml` |
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

Defect 10: no template provisions the binaries its checks invoke. F54 measures 22 standalone binaries the section 4.7 matrix invokes that no template installs (measured 2026-08-26). `actionlint` sits outside this population, because the section 4.7 matrix names it in no language column. F82 counts it under the workflow track; SC39 owns its provisioning, documented in its own subsection below.

**Install mechanism.** Nineteen of the 22 reach a mise backend, so each gains a row in the target root's `mise.toml` `[tools]` block and installs through `mise install --locked` — the same activation step in section 4. Eleven of those record a per-platform digest in `mise.lock`; eight do not (the backend, not the tool, decides — F63 names `go:`, `pipx:` and `core:rust` as backends that lock nothing). The remaining three reach no mise backend and install through the system package manager or a pinned from-source build (section 6). Every tool is pinned at its latest stable version, never `latest` (OD49).

**Digest verification is best-effort (SC7).** A tool arrives verified where its backend records a per-platform digest, and unverified where none does. A prebuilt download carries a digest; a tool the package manager assembles on the machine has no whole file to hash. The check is simply not run for those tools. No tool is named an exception, because the rule is a property of the backend rather than a carve-out for a name.

**The locking shape (F61).** A locking `mise.lock` row records a per-platform digest for 7 platforms with 0 skipped (`golangci-lint` via `aqua:golangci/golangci-lint` at 2.13.0 measured this). `bats-core` records 6 (every fleet target present; windows excluded).

### The 22-binary matrix

Digest column: **yes** = backend records a per-platform digest (11, F67); **no** = backend records none (8, F67); **system** = no mise backend, installs via the OS package manager or a pinned from-source build (3, F67/F79).

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
| `kcov` | Shell | none (system) | system | OD49 latest stable | apt build-deps + pinned from-source build — section 6 |
| `jq` | Shell | `aqua:jqlang/jq` | yes | OD49 latest stable | `mise install --locked` |
| `playwright` | Python (`test e2e`) | `pipx:` (1.62.0) or `npm:` (1.62.1) | no | OD49 latest stable | `mise install --locked`; ships its own Chromium (OD61) |
| Google Chrome | Go (`test e2e`) | none (system) | system | OD56 latest Chrome | apt (Google repo) / brew — section 6 |

Eleven lock, eight do not, three reach no mise backend (F67). The workflow track's own binary, `actionlint`, is counted separately by F82 and provisioned under SC39, documented in its own subsection below. `playwright` needs no browser install step: it ships its own Chromium (OD61), so the Python `test e2e` leg installs no Chrome.

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

### The workflow-track binary (SC39)

`actionlint` is the workflow track's own check tool, provisioned by `ci-workflow.yml` alone. It pins at `aqua:rhysd/actionlint` `1.7.12` — the version E8 records locking all seven platform entries with a SHA-256 and a provenance attestation — from a template-owned mise config, never a caller's committed `mise.toml`/`mise.lock` (the same shape as section 5's `mise install --locked`, scoped to one `MISE_CONFIG_FILE`).

The conversion step (section 10) also needs `jq` to read the check's own JSON result record. `jq` is already in the 22-binary matrix above at `aqua:jqlang/jq` `1.8.2` for the shell track; `ci-workflow.yml` installs the same pin a second time from its own config. A second install of an already-counted binary adds no binary to the fleet total (K10).

```yaml
- name: Provision workflow check binaries (actionlint, jq)
  env:
    MISE_CONFIG_FILE: ${{ runner.temp }}/workflow-check-tools.toml
  run: |
    set -euo pipefail
    cat > "${MISE_CONFIG_FILE}" <<'EOF'
    [tools]
    "aqua:rhysd/actionlint" = "1.7.12"
    "aqua:jqlang/jq" = "1.8.2"
    EOF
    mise trust "${MISE_CONFIG_FILE}"
    mise install
    mise bin-paths >> "$GITHUB_PATH"

- name: Installed check-tool versions (OD60)
  run: |
    set -euo pipefail
    actionlint -version
    jq --version
```

---

## 6. System-package tools

Three of the 22 reach no mise backend and install through the system package manager or a pinned from-source build (OD57, F79 measured 2026-08-26): `checkbashisms`, Google Chrome, and `kcov`.

| Tool | OS | Channel | Source / package | Serves |
|---|---|---|---|---|
| `checkbashisms` | Ubuntu | apt | `devscripts` package | Shell track (`source-checks` on `ubuntu-24.04-arm`) |
| `checkbashisms` | macOS | Homebrew | `checkbashisms` formula | Not installed — source checks run on `ubuntu-24.04-arm` alone (OD63) |
| Google Chrome | Ubuntu | apt | Google's own apt repository | Go `test e2e` on both Ubuntu targets |
| Google Chrome | macOS | Homebrew | `google-chrome` cask | Go `test e2e` on the macOS `build`/`test` legs (OD63) |
| `kcov` | Ubuntu | apt build-deps + source build | `SimonKagstrom/kcov` pinned to the v43 tag's own commit, built with cmake | Shell track (`source-checks` on `ubuntu-24.04-arm`) |

**The Google apt repository (F79).** `dl.google.com/linux/chrome/deb/dists/stable/Release` answers HTTP 200 with an `Architectures` line reading `amd64 arm64`. Both `main/binary-amd64/Packages` and `main/binary-arm64/Packages` answer 200; the counter-probe `main/binary-i386/Packages` answers 404. The arm64 index lists `google-chrome-stable`, so the channel covers both Ubuntu targets.

**`kcov` builds from source (defect 10 correction).** `SimonKagstrom/kcov`'s v43 release ships no downloadable binary for any platform, and Ubuntu noble's own apt archive carries no `kcov` package either, on any architecture. `mise`'s `ubi:` backend, which resolves a tool from its GitHub releases, therefore fails closed with no asset to select — apt carries no fallback package either. The template builds `kcov` from source instead: apt installs the build toolchain and coverage-backend headers (`build-essential`, `cmake`, `pkg-config`, `binutils-dev`, `libelf-dev`, `libdw-dev`, `libiberty-dev`, `libssl-dev`, `libcurl4-openssl-dev`, `zlib1g-dev`), then `cmake`, `cmake --build` and `cmake --install` compile and install the binary. The build pins the v43 tag's own commit rather than the branch tip, so a later commit on the default branch cannot change what the runner installs.

**Split by OS (OD63).** The source checks run on `ubuntu-24.04-arm`, so `checkbashisms` and `kcov` install there and need no macOS build. The Homebrew leg serves the macOS `build` and `test` legs alone — which for Chrome is the Go `test e2e` leg (OD56, OD64).

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

# Ubuntu — kcov via apt build-deps and a pinned from-source build (shell source-checks job,
# ubuntu-24.04-arm). v43 ships no binary release for any platform, and noble's own apt archive
# carries no kcov package either, so the template compiles it instead of fetching a binary.
- name: Install kcov (apt build-deps + source build)
  env:
    KCOV_COMMIT: a39874f938ce13f7a65f253120d1ec946b349ffe # v43, never the branch tip
  run: |
    set -euo pipefail
    sudo apt-get update
    sudo apt-get install -y build-essential cmake pkg-config \
      binutils-dev libelf-dev libdw-dev libiberty-dev libssl-dev \
      libcurl4-openssl-dev zlib1g-dev
    tmp="$(mktemp -d)"
    git clone --quiet https://github.com/SimonKagstrom/kcov.git "${tmp}/kcov"
    git -C "${tmp}/kcov" checkout --quiet "${KCOV_COMMIT}"
    cmake -S "${tmp}/kcov" -B "${tmp}/kcov/build"
    cmake --build "${tmp}/kcov/build" --parallel "$(nproc)"
    sudo cmake --install "${tmp}/kcov/build"
    rm -rf "${tmp}"
    kcov --version   # OD60: print what was installed

# Ubuntu — Google Chrome via Google's own apt repository (Go test e2e legs on both Ubuntu targets).
# gpg runs --batch --yes: the amd64 image already ships google-chrome.gpg, and a bare --dearmor
# asks to overwrite it on a /dev/tty a runner step has not got — which fails the step on amd64
# (gpg: cannot open '/dev/tty') while it passes on arm64, whose image ships no such file.
- name: Install Google Chrome (apt, Google repository)
  run: |
    set -euo pipefail
    curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
      | sudo gpg --batch --yes --dearmor -o /usr/share/keyrings/google-chrome.gpg
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
    # A cask drops the .app bundle under /Applications and puts no CLI on the Homebrew bin
    # prefix, so link the bundle's own executable onto the prefix (OD64): the copy that wins on
    # PATH is then the cask's, ahead of the image's own Chrome, and its --version equals it.
    ln -sf "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" "$(brew --prefix)/bin/google-chrome"
    "$(brew --prefix)/bin/google-chrome" --version   # OD60: print what was installed
```

---

## 7. The Homebrew PATH mapping

F80 measures Homebrew 6.0.13 present on the macOS image on 2026-08-26, with its PATH resolution **proven on Intel** through `/usr/local/bin` and **unproven on arm64**, where the prefix is `/opt/homebrew/bin`. No build script writes an `/etc/paths.d` entry for Homebrew, and the image's `bashrc` exports `/usr/local/bin` (the Intel prefix) but never `/opt/homebrew/bin` (the arm64 prefix).

**A macOS job maps the Homebrew prefix onto PATH before its first `brew` step.** The check step that runs `checkbashisms` or Chrome inherits the same mapping, because the binaries Homebrew installs land in that same prefix. Prefix by arch: `/opt/homebrew/bin` on arm64, `/usr/local/bin` on Intel. A **cask** is the exception: it installs an `.app` bundle under `/Applications` and puts nothing on the bin prefix, so the Chrome install step links the bundle's own executable onto the prefix (section 6) — without that link the mapping is inert and the resolution check below resolves an empty path.

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

Shared inputs across the seven templates. A common role takes a common name; only the target-root input varies by language, because each language names its root differently.

| Input | Type | Default | Templates | Role |
|---|---|---|---|---|
| `runs_on` | string | `"ubuntu-24.04"`* | all seven | Fully qualified runner label (section 1); never floating. |
| `module_dir` / `crate_dir` / `project_dir` / `script_root` / `target_root` | string | `"."` | Go / Rust / Python / shell / workflow | Target root `language-tools` checks, relative to the checked-out repo (`--dir`), except the Go unit pair, which passes it absolute as `${{ github.workspace }}/<input>` (section 1 note). Shell recurses from its root (OD54); workflow's adapter enumerates every tracked body at or below it (WFRULES clause (a)). |
| `mise_version` | string | measured latest stable patch (section 9) | five of seven | mise release the template installs. `ci-shell.yml` and `ci-workflow.yml` pin a step-scoped env var instead (section 9). |
| `dry_run` | boolean | `false` | six of seven | `true` installs and verifies mise + language-tools and stops; check steps and `extra_steps` are skipped. `ci-workflow.yml` carries none (below). |
| `test_timeout` | number | `300` | four CI templates | Seconds; defect 3 (below). |
| `extra_steps` | string | `""` | all seven | Shell script run after the checks, for a repository-specific check `language-tools` does not cover. Empty runs nothing. |

\* `ci-shell.yml` and `ci-workflow.yml` default to `"ubuntu-24.04-arm"` instead (section 1).

**`ci-workflow.yml`'s narrower input set.** It declares only `runs_on`, `target_root` and `extra_steps` — no `mise_version` (mise pins as a step-scoped env var, matching `ci-shell.yml`'s own precedent), no `dry_run`, and no `test_timeout` (defect 3 ties that input to a `test` subcommand, and this track's one pair is `lint`).

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
  run: language-tools test unit --language go --dir "${{ github.workspace }}/${{ inputs.module_dir }}" --log-dir "${{ github.workspace }}/.language-tools/log" --timeout "${{ inputs.test_timeout }}s"
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

**Two templates hardcode `MISE_VERSION` instead of exposing `mise_version`.** `ci-shell.yml` and `ci-workflow.yml` pin it as a step-scoped env var: neither track coordinates a per-repo toolchain pin, so there is no caller-visible dial to expose. Both still carry this section's own measured value. Re-confirmed unchanged for `ci-workflow.yml`'s 2026-09-06 edit date.

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

**Failure-path capture.** A `gate_negative.toolchain.error` reports only `<tool> exited N with no parsed diagnostics; see log_ref for raw output` in the capped result — the raw tool output the reader needs sits in the per-check record under `--log-dir`, which the run otherwise discards, so the error is untriageable from the run alone. Every CI check job (each of the five CI templates: `source-checks` and `build-test` for the three compiled-language templates, the single `checks` job for `ci-shell.yml` and `ci-workflow.yml`) therefore ends with one artifact-upload step, guarded `if: ${{ failure() }}`, that publishes the whole `--log-dir` tree when a prior step failed the job. It exports what a failed check already wrote — it runs no check, and changes no invocation, target root, subject set or verdict. The artifact name is scoped by language and job (and by `matrix.os` for the `build-test` matrix) so no two uploads in one self-test run collide (`upload-artifact@v4` rejects a duplicate name). Two limits this capture cannot lift, both language-tools-side and not the template's to fix: a multi-tool check routed in-process (Rust `security`, the `test` kinds) writes no sub-tool stdout/stderr into its record, so the captured log names which tool exited non-zero but not why; and a check whose failure is an infra fault before any record is written leaves nothing to upload (`if-no-files-found: ignore`).

```yaml
# Last step of every CI check job. Publishes the --log-dir tree on the failure path so a
# gate_negative.toolchain.error is triageable from the run. Name scoped by language + job (+
# matrix.os on build-test) for run-wide uniqueness. Runs no check; exports what one wrote.
- name: Capture language-tools logs on failure
  if: ${{ failure() }}
  uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
  with:
    name: language-tools-logs-go-source-checks
    path: ${{ github.workspace }}/.language-tools/log/
    if-no-files-found: ignore
```

### The workflow-track override (WFRULES clause (f))

`ci-workflow.yml` is the one template that converts two of the taxonomy's classes into a GitHub annotation instead of letting the step fail outright on any non-zero exit. `workflow lint`'s own two soft outcomes — a currency warning and a currency/rule-one error — surface inline on the pull request rather than send a reader to a log.

| Exit code | Status | Template behavior |
|---|---|---|
| `0` | `success` | Silent; step passes. |
| `10` | `caveats` | One `::warning::` annotation per `caveats[]` entry in the result record; step still passes. |
| `20` | `gate_negative` | One `::error::` annotation per `errors[]` entry in the result record; step fails. |
| anything else | — | Falls through to the general EXIT contract above: the step fails on the exit code language-tools reported, unmapped. |

The step captures the exit code through an `if`/`else` guard around the check invocation, so a 10 or a 20 never trips `set -e`, then reads the redirected stdout — the JSON result record every clikit CLI writes there — with `jq`. It never pattern-matches the check's log text. It adds no member to the closed clikit taxonomy: it only routes two of the eleven classes to an annotation, and every other class still fails the job exactly as the EXIT contract above states.

```yaml
- name: language-tools workflow lint
  run: |
    set -euo pipefail
    result="${RUNNER_TEMP}/workflow-lint-result.json"
    if language-tools workflow lint --dir "${{ inputs.target_root }}" --log-dir "${{ github.workspace }}/.language-tools/log" > "${result}"; then
      exit_code=0
    else
      exit_code=$?
    fi
    case "${exit_code}" in
      0)
        ;;
      10)
        jq -r '.caveats[] | "::warning::" + .message' "${result}"
        ;;
      20)
        jq -r '.errors[] | "::error::" + .message' "${result}"
        exit 20
        ;;
      *)
        echo "::error::language-tools workflow lint exited ${exit_code}; see ${result}"
        exit "${exit_code}"
        ;;
    esac
```

---

## 11. actionlint verification

Every YAML block above was extracted, wrapped in a `workflow_call` scaffold where it is a step or job fragment, and linted with `actionlint 1.7.12`. Result: **0 findings across all blocks.** The SC39 blocks added for `ci-workflow.yml`, the section 6 Chrome-provisioning blocks (non-interactive `gpg`, the cask-binary link), and the section 10 failure-path capture block all pass the same check. Re-run: extract each fenced `yaml` block, paste into a scratch workflow (steps under a `runs-on: ubuntu-latest` job; job maps under `jobs:`; the `on: workflow_call` input/env snippets under a minimal workflow header) and run `actionlint`.
