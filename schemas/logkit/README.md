# logkit — one log record, two renderings, three languages

The fleet's logging standard. A log call produces **one normalized record**; that record is rendered as one line of canonical JSON for machines and one line for a human, and Go, Rust and Python emit **the same bytes** for the same event.

This directory is the contract, and it lands before its implementations: `go/logkit` and `rust/logkit` in Phase A, `python/logkit` in Phase B. `clikit` consumes it for log output and does not redefine logging.

## Layout

| File | Role | Version |
|---|---|---|
| `logkit-logging.spec.md` | **The standard** — field semantics, level semantics and `Known()`, timestamp rules, normalization of native library output, dual rendering, versioning and back-compat. | `logkit@1.0.0` |
| `log-record.schema.json` | **Record schema** (JSON Schema 2020-12) — the normalized record's field names, types, required-vs-optional, the level enum and its severity ordinals. | `1.0.0` (`schema_version: 1`) |
| `logkit.contract.json` | **Contract data** — canonical serialization, timestamp derivation, the inbound level-alias map, per-writer configuration and non-equivalences, reserved `fields` keys, human-rendering tokens, stream defaults. | `logkit@1.0.0` |
| `examples/golden-records.jsonl` | Four records in canonical wire form, one per line — the byte-exact target for an implementation. | — |
| `examples/golden-records.human.txt` | The same four records in human form, in the same order. | — |

Read the spec first: the JSON files define names and types, the spec defines meaning.

The two golden files are one set of records in two forms: the human file is derivable from the JSONL file by applying `logkit.contract.json`'s `human_rendering` rules and nothing else. An implementation that reproduces both from its own records has implemented the dual rendering correctly.

## The record in one line

```json
{"level":"info","message":"index rebuilt","schema_version":1,"service":"navigator","timestamp":"2026-07-26T09:41:07.480Z"}
```

Five required fields — `schema_version`, `timestamp`, `level`, `service`, `message` — plus optional `service_version`, `fields`, `error` and `caller`. The root is **closed**: `fields` is the only extension point, so a consumer can validate strictly and a producer cannot smuggle an undeclared key.

## What the standard pins

- **Five levels, closed:** `debug` `info` `warn` `error` `fatal`, with severity ordinals beside the enum. A level from outside the process is normalized then admitted by `Known()`; an unknown token fails by name and never defaults.
- **RFC 3339 UTC, millisecond, fixed width** — `2026-07-26T09:41:07.480Z`. Truncated, never rounded; a timestamp with no timezone is rejected rather than assumed.
- **RFC 8785 (JCS) canonical JSON** — sorted keys, ECMA-262 numbers, minimal escaping, no whitespace, one record per `LF`-terminated line. This is what makes three languages byte-identical.
- **The native libraries are not equivalent by default.** zerolog, `tracing` and structlog disagree on levels, level case, message key, timestamp presence and timezone, key order, and whether fatal kills the process. logkit owns the record; the native library is only the writer.
- **Two renderings, one record.** Machine JSON is the wire contract; the human line is for people and nothing parses it.

## Self-validation

```sh
python3 -m json.tool schemas/logkit/log-record.schema.json >/dev/null   # well-formed
python3 -m json.tool schemas/logkit/logkit.contract.json   >/dev/null   # well-formed
```

The contract's internal and cross-file invariants — the root field list matches the schema's own properties, the severity ordinals cover exactly the level enum, every alias target is a real level and every level is reachable by its own name, the reserved `fields` keys match the schema, and the contract points at this schema's version — are stdlib-checkable:

```sh
python3 -c '
import json
s = json.load(open("schemas/logkit/log-record.schema.json"))
c = json.load(open("schemas/logkit/logkit.contract.json"))
levels = set(s["$defs"]["level"]["enum"])
assert set(s["properties"]) == set(s["$defs"]["root_field_name"]["enum"]), "root field list drifted"
assert set(s["$defs"]["severity"]["const"]) == levels, "severity ordinals drifted from the level enum"
assert {m["level"] for m in c["level_normalization"]["map"]} <= levels, "alias maps to a non-level"
assert levels <= {m["token"] for m in c["level_normalization"]["map"] if m["equivalent"]}, "a level is not reachable by its own name"
reserved = {e["key"] for e in c["reserved_field_keys"]}
assert reserved == set(s["properties"]["fields"]["properties"]), "reserved fields keys drifted from the schema"
assert c["level_normalization"]["lossy_token_field"] in reserved, "the lossy-token field is not reserved"
assert c["record_schema"]["$id"] == s["$id"], "contract points at another schema"
assert c["record_schema"]["version"] == s["version"], "contract pins another schema version"
assert c["record_schema"]["schema_version"] == s["properties"]["schema_version"]["const"], "record MAJOR drifted"
print("logkit contract invariants: ok")
'
```

The goldens are canonical bytes, checkable without a validator:

```sh
python3 -c '
import json
lines = open("schemas/logkit/examples/golden-records.jsonl").read().splitlines()
for n, line in enumerate(lines, 1):
    assert json.dumps(json.loads(line), sort_keys=True, separators=(",", ":"), ensure_ascii=False) == line, n
print(len(lines), "golden records: canonical")
'
```

Schema-validating them needs a validator, which belongs in an isolated environment — never the system interpreter:

```sh
python3 -m venv .venv && .venv/bin/pip install jsonschema
.venv/bin/python -c '
import json
from jsonschema import Draft202012Validator
schema = json.load(open("schemas/logkit/log-record.schema.json"))
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema)
for n, line in enumerate(open("schemas/logkit/examples/golden-records.jsonl"), 1):
    for e in validator.iter_errors(json.loads(line)):
        raise SystemExit("line %d: %s: %s" % (n, e.json_path, e.message))
print("golden records: valid")
'
```

## Versioning

Released as `schemas/logkit/vX.Y.Z` — the tag prefix equals the module's path from the repository root, with no language segment (`tooling/version-guard` enforces it). Consumers pin a released tag, never this directory's `HEAD`.

`schema_version` on the record is the record contract's **MAJOR**. Any change to the set of records that validate — including adding a level or a top-level field — is a MAJOR; the top-level field set is frozen for the life of a MAJOR, and all extension goes through `fields`. A consumer built against MAJOR *N* refuses a record declaring more than *N* rather than guessing. Full change classes and consumer expectations: `logkit-logging.spec.md`, "Versioning and back-compat".
