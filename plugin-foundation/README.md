---
name: plugin-foundation
description: Shared forced-use plugin foundation (routing rules, download-script, PreToolUse hook, adoption wiring) every govern-now CLI's plugin reuses instead of hand-authoring.
id: doc:plugin-foundation:overview
tags:
  - type:doc
  - topic:toolbelt
  - topic:forced-use
  - status:published
links:
  - go/adoption
updated: 2026-07-28T00:00:00Z
---

# plugin-foundation

The shared foundation every govern-now CLI's Claude Code plugin reuses to force-route Claude
toward its CLI instead of a raw OS tool (SC-FORCEDUSE), distribute its binary (SC-DISTRIBUTION),
and measure adoption through the shared `go/adoption` library (SC-LIBFIRST). A plugin authors one
data file — `routing-rules.json` — and wires in the two scripts below verbatim. It never copies
this directory, never hand-writes a Classify closure, and never hand-writes its own hook.

## Why one file drives everything

`routing-rules.json` (schema: `routing-rules.schema.json`) is the single source of truth for what
counts as this plugin's CLI usage versus the raw usage it supersedes. Three consumers read it:

- `forced-use-hook.sh` — applies it live, in-session, to decide deny-and-redirect vs. fail-open.
- `BuildRegistry` (Go, this package) — turns it into the `[]adoption.GovernedOperation` registry
  `adoption.Classify` scores a transcript against, for CI-time adoption measurement.
- Any documentation or lint tooling a plugin author wants, since it's plain, schema-validated JSON.

Because the hook and the registry read the identical prefix-match rules from the identical file, a
plugin's live routing decision and its measured adoption rate can never drift apart — the
divergence a hand-maintained pair of implementations would eventually accumulate.

## Wiring a plugin to this foundation

1. Author `routing-rules.json` for the plugin (see `routing-rules.example.json`, validate against
   `routing-rules.schema.json`): one entry per governed operation, naming its CLI invocation prefix
   and the raw tool call it replaces.
2. Wire `download-script.sh` into the plugin's `SessionStart` hook (see its header comment for the
   `PF_*` env contract) so the plugin's pinned version is fetched, checksum-verified, and cached
   before any tool call happens.
3. Wire `forced-use-hook.sh` into the plugin's `PreToolUse` hooks for the tool names the routing
   rules name as raw routes (typically `Bash`, plus whatever else a raw route uses), pointing
   `PF_ROUTING_RULES` at the plugin's own `routing-rules.json`.
4. In the plugin's CI, call `plugin_foundation.LoadRoutingRulesFile` + `BuildRegistry` to get the
   registry `adoption.Classify` and `adoption.BuildReport` need for the adoption gate.

No plugin-specific logic lives in either script or in `BuildRegistry` — every CLI name, binary
name, and command prefix comes from that plugin's own `routing-rules.json`. This is what makes the
foundation reused rather than copied: the same `download-script.sh` and `forced-use-hook.sh` files,
byte-identical, install into every govern-now CLI's plugin.

## Files

- `routing-rules.schema.json` — JSON Schema for `routing-rules.json`.
- `routing-rules.example.json` — a worked example for documentation purposes (not a test fixture).
- `download-script.sh` + `download-script.test.sh` — provisioning (SC-DISTRIBUTION): reads the
  pinned version, downloads the matching per-OS/arch release archive, verifies its checksum
  against the tag's shared checksums file, extracts the binary, caches it (and its own digest)
  idempotently, exports the verified path.
- `forced-use-hook.sh` + `forced-use-hook.test.sh` — the PreToolUse hook (SC-FORCEDUSE):
  deny-and-redirects a raw invocation when the CLI is available, fails open (allows, silently) when
  it is not, and never denies a raw tool call by claiming the tool doesn't exist.
- `registry.go` (`package plugin_foundation`) — `LoadRoutingRulesFile` + `BuildRegistry`, the Go
  half that turns `routing-rules.json` into an `adoption` registry.
- `testdata/` — the frozen fixture set (`routing-rules.json`, a fixture transcript, hook-eval logs,
  a fixture release) `registry_test.go`/`adoption_test.go`/the two `*.test.sh` scripts exercise.
  Not to be confused with `routing-rules.example.json`, which documents rather than tests.

## Running the tests

```sh
go test ./...
bash download-script.test.sh
bash forced-use-hook.test.sh
```

## Hard floor

`forced-use-hook.sh` logs a `denies_tool_exists: false` `HookEvalRecord` for every decision it
makes, by construction — it never has an existence-denial code path to hit. `adoption.CheckFloor`
governs this at the report layer: any record with `denies_tool_exists: true` is a hard violation
that overrides an otherwise-passing adoption rate, so a plugin cannot trade the "raw tool still
exists" guarantee for a higher adoption number.
