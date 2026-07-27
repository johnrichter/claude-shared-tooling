# logkit — cross-language structured logging standard (`logkit@1.0.0`)

The one logging standard the tool fleet emits and reads. One log call produces **one record**; the record is rendered as **one line of canonical JSON** for machines and as **one human line** for a terminal, and three languages emitting the same record produce **the same bytes**.

Normative together with two files in this directory — read all three:

| File | Normative for |
|---|---|
| `log-record.schema.json` | The record: field names, types, required-vs-optional, the level enum, the severity ordinals, the timestamp form. |
| `logkit.contract.json` | The data the rules key on: canonical serialization, timestamp derivation, the inbound level-alias map, per-writer configuration and non-equivalences, reserved `fields` keys, human-rendering tokens, stream defaults. |
| this document | Semantics: what each field means, when each level applies, how normalization runs and what it does on failure, how the two renderings relate, how the contract versions. |

**Precedence.** For a name, a type, an ordinal or an enumeration, the JSON files are the oracle and this document is a rendering of them; for meaning, this document is the oracle. A disagreement is a defect in whichever file restates rather than defines — report it, do not resolve it locally.

**Implementations**, each landing after this contract: `go/logkit` and `rust/logkit` in Phase A, conformance-gated byte-for-byte against each other, and `python/logkit` in Phase B. An implementation disagreeing with this contract is an implementation defect.

## The record is the contract

A record is data. Everything a reader sees is a rendering of it.

- **One call, one record, N renderings.** The human line is never re-derived, re-timed or re-formatted from a second capture of the event. When both renderings are enabled, their values are identical by construction because they come from one in-memory record.
- **Only the record is a wire contract.** Machine consumers parse the JSON rendering. **Nothing parses the human rendering** — it is for people, and its layout may change in a MINOR release.
- **The root is closed.** `additionalProperties: false`, so a strict consumer can reject an unknown top-level key as the producer bug it is. All extension goes through `fields`.
- **Logs go to stderr by default.** stdout carries the CLI's structured result under the `clikit` contract; a log line there corrupts a caller parsing that result.

## Canonical fields

Types, patterns and required-vs-optional are pinned in `log-record.schema.json`; this table is what each field means and when it is present.

| Field | Required | Meaning |
|---|:--:|---|
| `schema_version` | yes | MAJOR of this record contract, currently `1`. Per-record, so a line found on its own is self-describing. |
| `timestamp` | yes | Wall-clock instant the event happened (not the instant it was written). RFC 3339, UTC, exactly three fractional digits. |
| `level` | yes | Severity, from the closed five-member set. |
| `service` | yes | The logical emitter — the CLI, daemon or job name, fixed at logger construction. A sub-component within it goes in `fields`, so one service never fragments into many names. |
| `message` | yes | What happened, as a short constant phrase. |
| `service_version` | no | Version of the emitting build when it knows one. Free-form: not every build carries a released version. |
| `fields` | no | Structured context — the variable data that would otherwise be interpolated into `message`. Omitted when empty. |
| `error` | no | The failure that caused the event: `message`, optional `kind`, optional `stack`. |
| `caller` | no | Source location of the log call: `file` (module-relative), `line`, optional `function`. |

Rules that the schema cannot state:

- **`message` is constant, `fields` is variable.** `"index rebuilt"` with `documents=1284` in `fields`, never `"index rebuilt with 1284 documents"`. An interpolated message cannot be grouped, counted or alerted on, and it is the single most common defect this standard exists to prevent.
- **A `fields` key never shadows a root field name.** Consumers that flatten a record into one namespace would silently overwrite the record's own field. The schema rejects the collision.
- **Authored `fields` keys are `snake_case`.** Keys carried in verbatim from a foreign producer keep their original spelling: every case transformation is ambiguous for acronyms, and an ambiguous transformation is not deterministic.
- **`native_level` is reserved** and written only by the normalization pass. It is the only reserved `fields` key; `logkit.contract.json` holds the complete list.
- **`error` and `level` are independent.** An error-level record need not carry `error`, and a warn-level record may. Never infer one from the other.
- **`error` is not `clikit`'s `Error`.** This is the log-side view of a failure. `clikit` defines its own error shape for a CLI's result, and the mapping between the two is fixed by the `clikit` contract, not here.
- **Absent, never null.** An optional field that has no value is omitted. `null` is legal only as a `fields` **value**, where it means "explicitly known to be nothing" — distinct from omitting the key.
- **A blob is not a field.** Anything large enough to need scrolling belongs in a file, with the path in `fields`. This is guidance, not a checked limit: the schema caps `message`, `error.message` and each stack frame at 4096 characters, and does not bound `fields` values at all.

## Levels

Exactly five, lowercase. `log-record.schema.json` `$defs/level` is the enumeration and `$defs/severity` the ordinals; the table below adds when to use each.

| Level | Severity | Use when |
|---|:--:|---|
| `debug` | 10 | Developer detail for reproducing behavior. Off in a normal run; may be high-volume. |
| `info` | 20 | An expected milestone worth a permanent record. |
| `warn` | 30 | A degradation that was handled — the run continues and its result is still correct. |
| `error` | 40 | An operation failed. The run may continue with a reduced or partial result. |
| `fatal` | 50 | The process cannot continue. This is the last record it writes. |

- **An expected negative is not an error.** A gate that answers "no", a search that matches nothing, a check that legitimately fails — these are the tool working. They log at `info` (or `warn` when they degrade something) and are reported through the `clikit` exit-code taxonomy. `error` is for the tool failing.
- **`fatal` has no side effect of its own.** It writes the record, flushes the sink and returns. It does not exit and does not panic — the caller terminates through the `clikit` exit-code taxonomy, so process exit codes stay in one taxonomy instead of being decided inside a logging library. Native libraries that exit on their fatal level (zerolog) are therefore never driven at that level; see Normalization.
- **Thresholds compare ordinals.** A record is emitted when `severity(level) >= severity(threshold)`. Default threshold: `info`. `fatal` cannot be filtered out — no threshold ranks above it.
- **Ordinals are spaced by ten** so a future MAJOR can interleave a level without renumbering the ones already in use. They are never serialized: the record carries the name.
- **Disabling is configuration, not a level.** There is no `off`, `disabled` or `none` member; a writer that emits nothing is a writer setting.

### `Known()` semantics

Each implementation exposes the level as a **closed type** carrying a `Known()` predicate — `Level.Known() bool` in Go, its idiomatic equivalent in Rust and Python. This is the house pattern for a closed enum, and it is the only gate through which a level enters the system.

- **`Known()` is true iff the value is byte-equal to one of the five canonical names.** It does not trim, does not case-fold and does not accept an alias: `INFO`, ` info`, `warning` and `trace` are all unknown.
- Every level arriving from outside the process — a `--log-level` flag, an environment variable, a config file, a foreign record — runs the normalization procedure in `logkit.contract.json` **first**, then `Known()`. Normalization is where case-folding and aliases are resolved; `Known()` is where the result is admitted or refused.
- **A false answer terminates the operation with a named error** quoting the offending token and its source. Nothing defaults to `info`, nothing picks the nearest neighbor, nothing passes the token through as-is.
- The emit API takes the level **type**, not a string, so a caller cannot pass a level through by spelling it. Where the type is string-backed (Go), a hand-constructed value can still hold an unknown string — which is precisely why `Known()` exists and why the emit path asserts it rather than trusting the type alone.
- **Adding or removing a member is a MAJOR change.** Downstream matches on the level are exhaustive, which is the property that makes them safe — and the property a quietly-added sixth member would break.

## Timestamps

`YYYY-MM-DDTHH:MM:SS.sssZ` — RFC 3339, always UTC, always exactly three fractional digits, always 24 characters.

- **Truncate, never round**, when the clock is finer than a millisecond. Rounding can place an event after one that happened later; truncation cannot.
- **UTC always.** A local or offset-bearing reading is converted. A reading with **no** timezone is rejected — never assumed to be local, never assumed to be UTC.
- **Fixed width and zero-padded**, so lexicographic order equals chronological order and a plain sort is a correct sort.
- **Wall clock, not monotonic.** Runtimes that carry a monotonic reading alongside the wall clock (Go) format the wall clock.
- Ordering between concurrent emitters is not guaranteed beyond what the timestamps state; the logger is safe for concurrent use, and interleaved records are ordered by their timestamps, not by arrival.

## Byte-identical serialization

Three independent implementations produce the same bytes because the serialization is a published standard, not a house convention: **RFC 8785, the JSON Canonicalization Scheme (JCS)**. `logkit.contract.json` carries the full rule set; the parts that bite.

From JCS itself:

- **Keys sorted at every depth** (UTF-16 code-unit order); array order is data and is preserved.
- **Numbers in ECMA-262 shortest round-trip form** — an integral float serializes as an integer (`1.0` → `1`). `NaN` and the infinities are not JSON and are refused at the API boundary, never coerced to a string or a null.
- **Minimal string escaping**, UTF-8, no byte-order mark.
- **No whitespace** anywhere in the record.

Layered on top by logkit, because JCS canonicalizes one value and says nothing about framing records into a stream:

- **No duplicate keys.** A key written twice on one record resolves last-write-wins before serialization.
- **Empty containers are omitted**, not emitted as `{}` or `[]`.
- **One record per line**, terminated by a single `LF`, written in **one write** so concurrent emitters never interleave partial lines.

Sorted keys put `timestamp` at the end of the line, which reads oddly to a human — that is what the human rendering is for. Canonical JSON is already the rule everywhere in this repo that bytes must match, so an implementation reuses one canonicalizer instead of maintaining a logging-specific one.

## Dual rendering

Both renderings come off one record. `examples/golden-records.jsonl` and `examples/golden-records.human.txt` are the same four records in each form, line-for-line, and are the byte-exact target for an implementation.

### Machine — one line of JSON

The canonical serialization above, as-is. This is the interop surface.

```
{"level":"info","message":"index rebuilt","schema_version":1,"service":"navigator","timestamp":"2026-07-26T09:41:07.480Z"}
```

### Human — one line per record

```
<timestamp> <LEVEL padded to 5> <service> <message>[ <attribute>]...
```

Attributes follow the record's own canonical key order, with `fields` expanded in place and `schema_version` omitted:

- `caller` → `caller=<file>:<line>`, plus `caller_function=<name>` when present.
- `error` → `error=<message>`, plus `error_kind=<kind>` when present. Stack frames follow on their own lines, indented two spaces — the only rendering that spans more than one line.
- each `fields` entry → `<key>=<value>`.
- `service_version` → `service_version=<value>`.

Values render bare when they contain no whitespace, `"`, `=` or control character; otherwise as their JSON string form. A non-scalar renders as its canonical JSON.

```
2026-07-26T09:44:12.007Z ERROR git-tools worktree create refused error="fatal: 'feat/x' is already checked out at '/w/a'" error_kind=git.ExitError branch=feat/x exit_code=128
  git/worktree.go:88 Create
  git/cmd.go:41 run
```

The timestamp is the same UTC string as the machine rendering — not a shortened local time, because a person reading logs from another host needs the instant the record states, and because one timestamp rule is cheaper to keep correct than two.

**Color** is optional, applies to the level token only, and is enabled only when the sink is a TTY, `NO_COLOR` is unset and configuration allows it. It adds SGR sequences and changes no other character, so stripping them recovers the colorless rendering byte-for-byte. Codes are in `logkit.contract.json`.

## Normalization

**The native libraries are not equivalent by default.** zerolog, `tracing` and structlog disagree on the level set, the level case, the message key, the timestamp presence, format and timezone, the key ordering, and whether the fatal path terminates the process. Their default output is *not* a logkit record and never becomes one by configuration alone. logkit owns the record; the native library is a **byte writer**.

Two directions, both deterministic:

**Emission** — logkit builds the record, canonicalizes it, and hands the finished line to the writer. logkit **sets** the writer's field names, timestamp layout and level handling explicitly rather than relying on library defaults, so an upstream default change cannot drift the wire format. Native side effects are not used: the writer is never driven at a level that exits or panics.

**Inbound normalization** — mapping output that a native library produced under its own settings:

1. Rename keys per that library's `inbound_key_map` in `logkit.contract.json`. A key with no canonical home is carried verbatim into `fields`.
2. Normalize the level: trim, ASCII-lowercase (only `A`–`Z`; a locale-sensitive lowercase maps `I` to `ı` under a Turkish locale, so `INFO` would not fold to `info`), then look it up in the alias map. `trace` → `debug` and `panic` → `fatal` are **not** equivalences, so the source token is preserved in `fields.native_level`; `warning` → `warn` and `critical` → `fatal` are renames and preserve nothing.
3. Parse the timestamp in whatever form the source produced — epoch, offset-bearing ISO 8601, a custom layout — and re-render it canonically. A timestamp with no timezone is rejected.
4. Move the message to `message`, the source location to `caller`, and the failure to `error`.
5. Canonicalize and emit.

**Failure is loud and named.** An unmapped level token, an untyped timestamp, a `fields` key colliding with a root field, or a record that fails the schema after normalization **fails with the offending token and its source named**. Normalization never defaults, never guesses a timezone, never drops the offending key to make the rest pass.

Per-library key maps and the full non-equivalence list live in `logkit.contract.json` under `native_writers`.

## Versioning and back-compat

This is a language-agnostic contract module: it releases as **`schemas/logkit/vX.Y.Z`** — the tag prefix equals the module's path from the repository root, with no language segment (the repo's tag-prefix guard enforces it). Every file here carries its own version, so a consumer pins a contract version rather than a checkout.

`schema_version` in the record is the **MAJOR** of the record contract, and only the MAJOR.

| Change | Class |
|---|---|
| Any change to the set of records that validate — a field added, removed, renamed or retyped; a level added or removed; a different timestamp form; different canonicalization | MAJOR |
| A normative addition that leaves every previously valid record valid — a newly reserved `fields` key with a defined meaning, a new inbound alias or key map, a human-rendering refinement | MINOR |
| Editorial only — wording, examples, clarification that changes no rule | PATCH |

Expectations for downstream consumers (`clikit`, the conformance suite, every CLI and log reader):

- **Pin a released tag.** Never track this directory's `HEAD`.
- **Refuse forward.** A consumer built against MAJOR *N* refuses a record declaring more than *N*, naming the version it read. It does not read the fields it recognizes and hope.
- **Emit one MAJOR.** A producer emits exactly one record version per build; it never mixes versions on one stream.
- **The top-level field set is frozen for the life of a MAJOR.** A new top-level field is a MAJOR change, which is precisely why the root can be closed and `fields` is the extension point. Extending through `fields` is free and needs no coordination.
- **Match `level` exhaustively.** An unrecognized level is an error, never a default — the same rule `Known()` enforces inside the library.
- **Tolerate unknown `fields` keys** and ignore the ones you do not consume. Never key behavior on the absence of an optional field without a documented default.
- **Never parse the human rendering.** It is not a wire format; its layout can move in a MINOR. Implementations must still render it identically to each other, which the goldens pin.
- **No field is removed within a MAJOR.** A superseded field is documented as deprecated and keeps validating until the next MAJOR.

## Not in v1

Stated so their absence reads as a decision, not an oversight. Each would need a MAJOR (a new root field) or arrives through `fields` (free, today).

| Not present | Why, and what to do meanwhile |
|---|---|
| Trace/span correlation ids | No consumer in this fleet has specified a correlation model yet, and inventing root fields for one would freeze a guess into a MAJOR. Carry them in `fields` until a consumer defines the model. |
| Host, process, thread identity | Not needed by a single-process CLI, and non-deterministic, which would complicate the conformance goldens. `fields` carries them where a daemon needs them. |
| Sampling and rate limiting | A writer policy, not a record property. A sampled-away record is not a different record. |
| Transport, routing, rotation | The sink's job. logkit produces bytes and hands them over. |
| Redaction and secret policy | Not specified here: what counts as sensitive is the caller's domain knowledge. The standing rule is unchanged — never log a credential, a token or a full request body. |
| Exit codes | `clikit` owns the exit-code taxonomy. A logging standard that also decided process exit codes would be two contracts in one file. |
