# clikit — CLI output, error and exit contract (`clikit@1.0.0`)

The one contract every CLI's stdout, stderr and exit status obeys. An invocation produces **one result record** on stdout, **one exit code** from a closed set of eleven, and **zero logging rules of its own** — log output is `logkit`'s, consumed by reference.

Normative together with two files in this directory — read all three:

| File | Normative for |
|---|---|
| `result-record.schema.json` | The record: field names, types, required-vs-optional, the status enum, the exit-code enum, the status/exit-code pairing, the diagnostic and triage shapes. |
| `clikit.contract.json` | The data the rules key on: the eleven classes with their use-when and boundary rules, the triage kinds, the reserved codes, the output bounds, the exempt surfaces, the logkit mapping. |
| this document | Semantics: what each field means, how a diagnostic is classified, what a triage directive promises, how the streams divide, how the contract versions. |

**Precedence.** For a name, a type, an integer or an enumeration, the JSON files are the oracle and this document is a rendering of them; for meaning, this document is the oracle. A disagreement is a defect in whichever file restates rather than defines — report it, do not resolve it locally.

**Implementations**, each landing after this contract: `go/clikit` and `rust/clikit` in Phase A, conformance-gated byte-for-byte against each other, a non-gated shell helper that shell surfaces call instead of hand-writing JSON, and `python/clikit` in Phase B. An implementation disagreeing with this contract is an implementation defect.

## What clikit owns, and what it delegates

| Concern | Owner |
|---|---|
| stdout: the result record, its fields, its bounds | clikit |
| The exit-code taxonomy | clikit |
| Structured errors, caveats and triage directives | clikit |
| stderr: log records, levels, timestamps, renderings | **`logkit@1.0.0`**, by reference |
| Canonical serialization of a record into bytes | **`logkit`'s `canonical_json`**, adopted by reference |
| The members of any command's `data` payload | that command |

Nothing about logging is defined, redefined or restated here. The single seam clikit owns is the mapping from a clikit diagnostic onto a logkit record — which logkit's own contract defers to this file — and it is in "Streams and logkit delegation" below.

## The result is the contract

- **One invocation, one record.** It is written to stdout immediately before exit, whatever the outcome. A caller that reads no record from a clikit CLI knows the process died outside clikit.
- **The record is not a mode.** No flag suppresses it, reshapes it or pretty-prints it: a caller must be able to parse the result without knowing which flags the invocation carried.
- **The record is a pure function of the invocation.** No clock reading, no host identity, no build version, no measured duration. Identical input yields identical stdout bytes — which is what makes a per-CLI golden-output test and the cross-language conformance gate possible at all. Everything time-varying belongs on the log stream, where it is already at home.
- **The root is closed.** `additionalProperties: false`, so a strict consumer can reject an unknown top-level key as the producer bug it is. All extension goes through `data`.
- **`status` and `exit_code` are one fact in two channels.** The integer is the coarse channel for a caller that cannot parse JSON; the name is what a JSON consumer matches exhaustively. The schema pins the pairing, one branch per class, so the two can never disagree.

## Fields

Types, patterns and required-vs-optional are pinned in `result-record.schema.json`; this table is what each field means and when it is present.

| Field | Required | Meaning |
|---|:--:|---|
| `schema_version` | yes | MAJOR of this record contract, currently `1`. Per-record, so a record found on its own is self-describing. |
| `command` | yes | The resolved command path, root command first: `["git-tools","worktree","create"]`. Canonical names only — an alias is resolved, and no flag or operand appears. |
| `status` | yes | The outcome class as a name, from the closed eleven-member set. |
| `exit_code` | yes | The integer the process exits with, paired with `status`. |
| `data` | no | The command's own answer. The only extension point. Omitted when the command has nothing to report. |
| `errors` | conditional | Why the outcome is not plain success. Present for every failure class, absent for `success` and `caveats`. |
| `caveats` | conditional | Qualifications on a result that is still usable. Required for `caveats`, forbidden for `success`, optional elsewhere. |

Rules the schema cannot state:

- **`command[0]` is the tool's identity and equals the `service` the same process gives logkit.** One process, one name, or a result and its logs cannot be correlated.
- **`data` is the answer, never the work.** Only the fields the task needs: never a captured subprocess stream, never a file's contents, never a diff or a log excerpt. Anything a reader would scroll rather than read is written to a file, and the path goes in `data`.
- **`data` members are the command's own contract**, declared by that CLI and validated by `schema` where it matters. clikit bounds the shape — object, snake_case field names, bounded size — and defines no member.
- **Absent, never null.** An optional field with no value is omitted. Never `{}`, never `[]`, never `null`.
- **Bounds are contract, not style.** The caller of these CLIs is usually a model with a context budget, and an unbounded result is the failure mode this contract exists to prevent. The schema caps `data` and `context` member counts, diagnostic array lengths, message lengths and code lengths; `clikit.contract.json` `bounds` adds the 64 KiB soft budget for the whole record and the truncation rule when a command has more diagnostics than the cap allows.

## Errors and caveats

Both arrays carry the **same** shape — `{code, message, triage}` plus optional `context` — and the array a member sits in says how it bears on the result:

- an **`errors`** member is a reason the outcome is not plain success;
- a **`caveats`** member qualifies a result the caller can still use.

**`triage` is required on both.** A diagnostic that does not tell the caller what to do next is not finished, and that includes a caveat: a caveat the caller cannot act on is noise.

### The code carries its class

`code` is the diagnostic's stable machine identity, and its **first segment is the status name it belongs to** — `conflict.worktree.branch_checked_out`, `caveats.toolchain.target_skipped`. There is no separate class field, so class and code cannot drift apart, and a code pasted into an issue or a log is self-describing.

- **Two to four snake_case segments**, recommended form `<class>.<domain>.<condition>`. `success` is not a valid prefix — class 0 carries no diagnostics.
- **The namespace below the class is open.** A tool adds codes freely; that is not a contract change. The **class set** is closed and changing it is MAJOR.
- **`<class>.clikit.*` is reserved to the library** in every class. `clikit.contract.json` `reserved_codes` holds the complete list.
- **Reclassifying a diagnostic changes both its exit code and its code.** That is deliberate: the class is part of what a caller branches on, so a reclassification is wire-visible either way.

### The governing error

`errors` is ordered **most-actionable-first**, and `errors[0]` is the **governing** error: its code's class equals the record's `status`, which the schema enforces. This is what makes the exit code derivable from the record rather than decided separately.

- When a run produces failures in several classes, the command **chooses** the governing one — the failure the caller must act on first — and orders `errors` accordingly. The choice is deterministic for identical input, like every other part of the record.
- **Never aggregate by picking the largest integer.** The taxonomy is a classification, not a severity ordering.
- **`caveats` may accompany `errors`.** A run that skipped three unreadable files and then found five policy violations reports both, and its class comes from the violations.

`message` is one line, in the caller's terms: no stack trace, no captured output, no restatement of the code. Variable detail goes in `context`.

## Triage directives

The directive is the field that makes a structured error worth more than a message. Three kinds, closed:

| Kind | Says | Carries |
|---|---|---|
| `reinvoke` | Run **this** CLI again, this way. | `command` (argv, `command[0]` is this tool), optional `after_seconds`, optional `instruction` |
| `run_tool` | Run **that** executable, this way. | `command` (argv, `command[0]` is another executable), optional `instruction` |
| `manual` | No invocation fixes this; a person must act. | `instruction` |

Rules:

- **Executable as given.** Concrete paths, branches and values — never a placeholder like `<branch>`, never a shell line with quoting or redirection. A directive the caller would have to edit before running is a `manual` with an instruction instead.
- **argv, not a shell string.** Each element is one already-unquoted argument, so a caller may exec it directly without a shell.
- **One directive, the best next step.** Not a menu: when several fixes exist, name the recommended one and leave the alternatives to `message` or `instruction`.
- **Advice, never an action.** The tool does not run its own directive, and emitting one has no side effect.
- **`after_seconds` is a floor**, not a schedule — the earliest a retry is worth attempting. Backoff across attempts is the caller's policy, and the field is meaningful only for a `reinvoke` of a transient failure.
- **`run_tool` is the sanctioned exit from the CLI-before-raw-OS-tool routing rule.** A CLI that will not do something names the exact tool and the exact invocation that will, so routing a caller to the CLI first never strands them. At class 80 this is effectively mandatory; use `manual` there only when no executable can do the work.

## The exit-code taxonomy

Eleven classes. The integer is the **coarse channel** — enough to branch on without parsing anything — and the record carries the full detail.

| Code | `status` | The outcome |
|---:|---|---|
| `0` | `success` | Did what was asked; the result is complete and unqualified. |
| `10` | `caveats` | Did what was asked and the result is usable, but qualified. |
| `20` | `gate_negative` | An expected negative: asked a question about something that exists, and the answer is no. |
| `30` | `precondition_unmet` | The state the operation requires is not in place; the operation was not attempted. |
| `40` | `not_found` | A subject the caller named does not exist. |
| `41` | `conflict` | The subject exists in a state incompatible with the request. |
| `50` | `usage` | The invocation itself is wrong. Nothing was attempted. |
| `60` | `transient` | An identical re-invocation may resolve it, with no change by anyone. |
| `70` | `permission` | Access is refused, and an identical re-invocation will be refused again. |
| `80` | `unsupported` | A well-formed request this tool does not serve, by scope, platform or version. |
| `90` | `internal` | The tool itself failed, or produced an outcome it cannot classify. |

Per-class **use-when** lists and the **full boundary rules against every confusable neighbour** are in `clikit.contract.json` `exit_taxonomy.classes`, one entry per class. The lines that decide most real cases:

- **20 is the tool working; 90 is the tool broken.** A check that finds violations, a gate that refuses, a comparison that is false — all succeeded at their job. This is the single most important line in the taxonomy, and collapsing it is the defect the taxonomy exists to prevent.
- **40 vs 30: whose thing is missing.** In the caller's argv → `40 not_found`. Something the command needs in order to work at all → `30 precondition_unmet`.
- **40 vs 41: absent vs present-in-the-wrong-state.**
- **50 vs 40: shape vs existence.** A malformed selector could not name anything → `50`. A well-formed selector that names nothing → `40`.
- **60 vs 70: retrying helps vs never helps.**
- **30 vs 60: someone must change the world vs it resolves on its own.**
- **80 vs 50: a valid request for something absent by design vs a request that is not valid.**
- **Unclassifiable is 90, never 60.** Stopping on an unknown failure is safer than retrying one.

Properties the taxonomy holds, and the schema enforces:

- **Exhaustive.** Every terminating outcome falls in exactly one class; an outcome fitting none of them is `internal`. The set is never extended in the field.
- **Non-overlapping.** Each class states where it ends against the classes it could be confused with.
- **A classification, not an ordering.** `90` is not "worse" than `41`. Never compare these integers with `<` or `>`.
- **1, 2, 126, 127 and 128+N are unused on purpose.** They belong to the runtime and the shell, so a code from a clikit CLI is never ambiguous with a panic, a `command not found` or a signal death — and a caller seeing one knows the process died outside clikit and that there is no record to read.

For callers:

- Branch on the integer when you cannot parse JSON; read `status` and `errors[0]` when you can. Both are always present and always agree.
- **`if cmd; then` treats `10` as failure.** A caller asking "did it produce a usable result" tests `code == 0 || code == 10`.
- Treat an out-of-taxonomy code as an unclassified tool failure. Do not map it into the taxonomy.

## Streams and logkit delegation

| Stream | Carries |
|---|---|
| stdout | Exactly one result record, canonical JSON, LF-terminated, written immediately before exit. Nothing else, ever. |
| stderr | logkit records only, in whichever of logkit's two renderings is configured. |

A CLI that cannot write its record — stdout closed, disk full — exits `90` with no record.

**Serialization is logkit's, adopted by reference:** RFC 8785 (JCS) key ordering, numbers, strings and encoding, plus logkit's no-duplicate-keys, omitted-empty-containers, one-record-per-LF-terminated-line and single-write framing. It is not restated here. Two canonicalizations in one fleet would be two to keep correct, and both conformance suites reuse one canonicalizer.

**The diagnostic-to-log-record mapping** (logkit's contract defers this to clikit):

| clikit | logkit |
|---|---|
| `error.message` | `error.message`, verbatim — both contracts bound a message at 4096 characters and forbid control characters |
| `error.code` | `fields.clikit.error_code` — **not** `error.kind`, which means the failure's type as the emitting language names it and may be present alongside |
| `error.context` members | `fields` members of the same name; a member colliding with a logkit root field name is nested under the reserved `clikit` key rather than dropped or renamed |
| `error.triage` | not logged — a directive is for the caller reading the result, and duplicating it would make the log a second place a fix is stated |

`clikit` is the one `fields` key clikit reserves in a logkit record; its members are pinned in `clikit.contract.json` `logkit.reserved_log_field`. One nested key rather than several flat ones, because callers already use flat names like `exit_code` for a subprocess's status.

**The terminating log record.** Immediately before exit, a CLI writes exactly one logkit record carrying `fields.clikit.exit_code` and `fields.clikit.status`, at the level its class maps to (`clikit.contract.json` `exit_taxonomy.classes[].log_level`): `info` for `success` and `gate_negative` — an expected negative is not an error — `warn` for `caveats`, `error` for the caller- and environment-caused failures, `fatal` for `internal`. A command with more context may log **higher** than the default, never lower.

Class 90 logging at logkit's `fatal` is exactly why that level has no side effect of its own: it writes, flushes and returns, and the process exits through this taxonomy. Exit codes live in one contract instead of inside a logging library.

### One failure, both streams

stdout — the result, and the process exits `41`:

```json
{"command":["git-tools","worktree","create"],"errors":[{"code":"conflict.worktree.branch_checked_out","context":{"branch":"feat/x","worktree":"/w/a"},"message":"branch 'feat/x' is already checked out at '/w/a'","triage":{"command":["git-tools","worktree","create","--branch","feat/x-2"],"kind":"reinvoke"}}],"exit_code":41,"schema_version":1,"status":"conflict"}
```

stderr — the terminating logkit record for the same failure, in logkit's machine rendering:

```json
{"error":{"kind":"git.ExitError","message":"branch 'feat/x' is already checked out at '/w/a'"},"fields":{"branch":"feat/x","clikit":{"error_code":"conflict.worktree.branch_checked_out","exit_code":41,"status":"conflict"},"worktree":"/w/a"},"level":"error","message":"worktree create refused","schema_version":1,"service":"git-tools","timestamp":"2026-07-26T09:44:12.007Z"}
```

`service` matches `command[0]`, the context members land as `fields` of the same name, `error.kind` keeps its logkit meaning (the Go type) while the clikit code sits under the reserved `clikit` key, and the timestamp exists only on the log side. The directive appears once, on stdout.

## Exempt surfaces, and adapting the command framework

Three surfaces are the command framework's, not operations, and emit **no** record: `--help`/`-h`, `--version`, and generated shell completions. Each prints its text on stdout and exits `0`. Their consumer is a shell or a person, not a caller parsing a result. The exemption is exactly this list and a CLI does not extend it.

Everything else emits a record — including a rejected invocation, which means **the framework's own error path is disabled**. cobra and clap both print usage text to stderr and exit `1` by default, which is outside this taxonomy. An implementation turns off framework error printing, usage printing and the framework's own exit call, catches the parse failure, and emits a class-`50` record through clikit. Shell surfaces call the clikit helper or the CLI and never hand-write JSON.

## Versioning and back-compat

This is a language-agnostic contract module: it releases as **`schemas/clikit/vX.Y.Z`** — the tag prefix equals the module's path from the repository root, with no language segment (the repo's tag-prefix guard enforces it). Every file here carries its own version, so a consumer pins a contract version rather than a checkout.

`schema_version` in the record is the **MAJOR** of the record contract, and only the MAJOR.

| Change | Class |
|---|---|
| Any change to the set of records that validate — a field added, removed, renamed or retyped; a status or exit code added, removed or renumbered; a triage kind added or removed; a bound tightened; different canonicalization | MAJOR |
| A normative addition that leaves every previously valid record valid — a newly reserved code with a defined meaning, a boundary rule clarified into a decision it already implied, a bound relaxed, a rendering added | MINOR |
| Editorial only — wording, examples, clarification that changes no rule | PATCH |

Adding an exit class or a triage kind is MAJOR for the same reason adding a logkit level is: downstream matches on both are exhaustive, and exhaustiveness is the property that makes them safe. Adding an error **code** within an existing class is not a contract change at all.

Expectations for downstream consumers (every CLI, the conformance suite, every caller parsing a result):

- **Pin a released tag.** Never track this directory's `HEAD`.
- **Refuse forward.** A consumer built against MAJOR *N* refuses a record declaring more than *N*, naming the version it read.
- **Emit one MAJOR.** A build emits exactly one record version.
- **The top-level field set is frozen for the life of a MAJOR**, which is why the root can be closed and `data` is the extension point. Extending through `data` is free and needs no coordination.
- **Match `status` and `triage.kind` exhaustively.** An unrecognized member is an error, never a default.
- **Tolerate unknown `data` and `context` keys** and ignore the ones you do not consume.
- **Never key behavior on an error's message text.** The `code` is the stable identity; the message is for people and may be reworded in a PATCH.
- **No field is removed within a MAJOR.** A superseded field is documented as deprecated and keeps validating until the next MAJOR.
- **Pin the logkit MAJOR too.** `clikit@1.0.0` consumes `logkit@1.x`, record MAJOR `1`.

## Not in v1

Stated so their absence reads as a decision, not an oversight.

| Not present | Why, and what to do meanwhile |
|---|---|
| A human rendering of the result | No consumer has specified one, and the narration a person at a terminal needs is already logkit's human rendering on stderr. Adding one later changes no record and would be a MINOR. |
| The emitting build's version in the record | It would make stdout differ between two builds, and between the Go and Rust implementations of the same command — the exact determinism the conformance gate rests on. logkit's `service_version` carries it on the log stream. |
| Timing, duration and progress | Same reason: they are time-varying. Progress and durations are logkit `fields` on stderr; a long operation reports there. |
| Streaming or progressive results | One record per invocation, written at exit. A streaming result would need a framing contract no consumer has asked for. |
| A registry of per-command `data` schemas | Each command's payload is its own contract, declared and versioned by its CLI. clikit bounds the shape; centralizing the members would make this file change every time any command does. |
| Exit codes for signals, panics and shell failures | Deliberately outside the taxonomy — see `1, 2, 126, 127, 128+N` above. Callers detect them as "no record". |
| Localization of messages | Messages are one language, and callers key on `code`, never on text. |
