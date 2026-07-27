---
name: clikit shell helper
description: Thin shell wrapper that delegates JSON emission to go/clikit; the only sanctioned way shell code produces clikit records.
id: doc:clikit:shell-helper
tags:
  - type:doc
  - topic:clikit
  - topic:shell
  - status:published
links:
  - schemas/clikit/clikit-cli-contract.spec.md
updated: 2026-07-26T17:00:00Z
---

# clikit shell helper

This directory provides `clikit-emit`, the **only sanctioned way shell code produces clikit records**. Shell surfaces call it instead of hand-writing JSON.

## Why

Emitting clikit records correctly requires precise JSON serialization (RFC 8785 key ordering, no duplicate keys, omitted empty containers), structured-error validation, and status/exit-code pairing enforcement. These rules live in one place — the `go/clikit` library — not repeated across every shell surface.

## What

`clikit-emit` is a thin wrapper that delegates all JSON emission to a clikit emitter (a built executable or library binary). It takes structured input (command path, status, exit code, error/caveat diagnostics with optional triage directives) as command-line arguments and invokes the emitter to produce the record.

The record is written to stdout, the process exits with the specified code, and stderr carries logkit records if any were emitted during the operation.

The helper never hand-writes JSON — that is the reason it exists. When the emitter binary is not available it cannot produce a record, so, per the clikit contract's rule that a surface which cannot write its record exits `90` with no record, it prints a diagnostic to stderr and exits `90` with empty stdout.

## Usage

```bash
clikit-emit [OPTIONS]
```

### Options

- `--command PARTS...` — Resolved command path, comma-separated. Example: `--command git-tools,worktree,create`
- `--status STATUS` — Outcome class name. One of: `success`, `caveats`, `gate_negative`, `precondition_unmet`, `not_found`, `conflict`, `usage`, `transient`, `permission`, `unsupported`, `internal`
- `--exit-code NUM` — Exit code integer. One of: `0`, `10`, `20`, `30`, `40`, `41`, `50`, `60`, `70`, `80`, `90`
- `--data JSON` — Optional command data payload (JSON object)
- `--error CODE MSG` — Add an error diagnostic: error code (required), message (required)
- `--error-context K V` — Add context to the last error (key, value pairs; repeatable)
- `--error-triage K V` — Add a triage directive to the last error (key-value pairs; forms a triage object)
- `--caveat CODE MSG` — Add a caveat diagnostic: code (required), message (required)
- `--caveat-context K V` — Add context to the last caveat (key, value pairs; repeatable)
- `--caveat-triage K V` — Add a triage directive to the last caveat (key-value pairs)

### Examples

See `USAGE-EXAMPLE.sh` for runnable examples. Once a clikit emitter binary is built and available, the examples demonstrate the expected output produced by delegation to the Go or Rust emitter implementation, conforming to the contract in `schemas/clikit/clikit-cli-contract.spec.md`.

## Not conformance-gated

This helper is **not subject to the cross-language conformance gate** that proves Go and Rust implementations emit byte-identical output. It is a thin shell wrapper that forwards all work to the Go implementation, so it inherits Go's correctness by delegation, not by independent verification.

Every shell surface that produces a clikit record must call `clikit-emit` or the `go/clikit` CLI directly — never hand-write JSON.

## Contract reference

See `schemas/clikit/clikit-cli-contract.spec.md` for the full clikit contract:

- Result record schema and field meanings
- The eleven exit classes and when each applies
- Diagnostic structure (errors and caveats)
- Triage directive kinds (reinvoke, run_tool, manual)
- Streams and logkit delegation
- Versioning and back-compat rules

## Current state (missing dependency)

The helper is complete, but the emitter binary it delegates to does not yet exist. The sibling tasks M2.P4.T1 (Go library) and M2.P4.T2 (Rust library) provide the emission logic but no CLI binary or exported emitter entry point, and no task in the plan builds one. A follow-up task is required to:

1. Build a standalone emitter CLI (`main` wrapping `go/clikit`) exposing the `emit` subcommand and place it at the expected path, OR
2. Provide an emitter entry point that shell code can invoke.

Until the emitter exists, `clikit-emit` cannot emit a record: it prints a diagnostic to stderr and exits `90` with empty stdout. It never hand-writes a record. The integration tests that exercise real emission are skipped until the binary is present.

## Installation

Place `clikit-emit` on `$PATH`, or source it and call the script by absolute path. The script resolves the emitter binary relative to its own location.

If the emitter binary is not found at the default location (`../../go/.bin/clikit` relative to this script), set the `CLIKIT_BIN` environment variable to the binary path:

```bash
CLIKIT_BIN=/usr/local/bin/clikit-emitter clikit-emit --command ...
```

## Emitter availability

Currently, the emitter binary is not built by any task in the plan. Shell surfaces calling `clikit-emit` will get exit `90` and a stderr diagnostic (no stdout record) until the emitter is built. See "Current state" above.

## Rationale

Shell surfaces are common in the tooling ecosystem — hooks, build steps, integration points. Asking each to implement clikit emission correctly (including RFC 8785 canonicalization, structural validation, and pairing rules) would be error-prone and impossible to verify. By providing a single, tested helper that delegates to one library, we ensure:

1. **Correctness by delegation** — the Go library handles all JSON emission; shell scripts cannot deviate.
2. **No duplication** — one place where the contract rules live.
3. **Easy auditing** — a reviewer checking for hand-written JSON knows any shell surface must route through `clikit-emit`; hand-rolled JSON anywhere is a defect.
4. **Consistency** — every shell surface produces records bit-identical to the Go CLI when given the same input.
