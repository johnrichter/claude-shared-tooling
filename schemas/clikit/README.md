# clikit — one result record, eleven exit codes, logging delegated

The contract every CLI's stdout, stderr and exit status obeys. An invocation writes **one bounded result record** to stdout, exits with **one of eleven codes**, and states **no logging rule of its own** — log output is `logkit`'s.

This directory is the contract, and it lands before its implementations: `go/clikit` and `rust/clikit` in Phase A (gated byte-for-byte against each other by `conformance/clikit`, which also judges its own goldens against the files here) plus a non-gated shell helper, and `python/clikit` in Phase B. It consumes `schemas/logkit` and does not redefine logging.

## Layout

| File | Role | Version |
|---|---|---|
| `clikit-cli-contract.spec.md` | **The standard** — field semantics, diagnostic and triage semantics, the taxonomy's decision lines, stream division, logkit delegation, versioning and back-compat. | `clikit@1.0.0` |
| `result-record.schema.json` | **Record schema** (JSON Schema 2020-12) — field names, types, required-vs-optional, the status and exit-code enums, the pairing between them, the diagnostic and triage shapes. | `1.0.0` (`schema_version: 1`) |
| `clikit.contract.json` | **Contract data** — the eleven classes with use-when and boundary rules, triage kinds, reserved codes, output bounds, exempt surfaces, the logkit mapping. | `clikit@1.0.0` |
| `examples/golden-results.jsonl` | Eleven records in canonical wire form, one per line — one per exit class, in code order. | — |

Read the spec first: the JSON files define names, types and integers, the spec defines meaning.

## The record in one line

```json
{"command":["navigator","search"],"data":{"hits":3,"matched_paths":["agent/identity.md","agent/workflows/index.md","README.md"],"query":"tag:apm"},"exit_code":0,"schema_version":1,"status":"success"}
```

Four required fields — `schema_version`, `command`, `status`, `exit_code` — plus optional `data`, `errors` and `caveats`. The root is **closed**: `data` is the only extension point, so a consumer can validate strictly and a producer cannot smuggle an undeclared key.

## What the standard pins

- **Eleven exit codes, closed:** `0` success · `10` caveats · `20` gate-negative · `30` precondition-unmet · `40` not-found · `41` conflict · `50` usage · `60` transient · `70` permission · `80` unsupported · `90` internal. The integer is the coarse channel for a caller that cannot parse JSON; the record carries the full detail. `1`, `2`, `126`, `127` and `128+N` are unused on purpose — they are the runtime's and the shell's.
- **A classification, not an ordering.** `90` is not worse than `41`; the integers are never compared with `<` or `>`. Exhaustive and non-overlapping: every class states where it ends against the classes it could be confused with, and an outcome fitting none of them is `internal`.
- **`status` and `exit_code` cannot disagree.** The schema pins the pair with one branch per class, and the same branch fixes which of `errors` and `caveats` may appear.
- **Every diagnostic ends in a triage directive** — required on errors *and* caveats: `reinvoke` (run this CLI this way), `run_tool` (run that executable this way) or `manual` (a person must act). Executable as given: concrete argv, never a placeholder, never a shell line.
- **The code carries its class.** A diagnostic's first code segment is its status name, so class and code cannot drift and no separate class field exists.
- **Bounded output is a contract property**, not a style preference: only the fields the task needs, never a raw command dump, with member counts and lengths capped in the schema and a 64 KiB soft budget for the record.
- **The record is a pure function of the invocation** — no clock, no host, no build version — so identical input yields identical bytes. That is what makes golden-output tests and the cross-language conformance gate possible.
- **Logging is `logkit@1.0.0`, by reference.** clikit owns exactly one thing at the seam: how a clikit diagnostic maps onto a logkit record, which logkit's contract defers to this one.

## Self-validation

```sh
python3 -m json.tool schemas/clikit/result-record.schema.json >/dev/null   # well-formed
python3 -m json.tool schemas/clikit/clikit.contract.json      >/dev/null   # well-formed
```

The contract's internal and cross-file invariants — the taxonomy covers exactly the schema's status and exit-code enums in the same order, the schema's pairing branches agree with it, the triage kinds match, every reserved code names a real class and sits in the reserved namespace, the contract points at this schema's version, the goldens cover every class exactly once, and the pinned logkit MAJOR is the one logkit ships — are stdlib-checkable:

```sh
python3 -c '
import glob, json, re
s = json.load(open("schemas/clikit/result-record.schema.json"))
c = json.load(open("schemas/clikit/clikit.contract.json"))
statuses, codes = s["$defs"]["status"]["enum"], s["$defs"]["exit_code"]["enum"]
classes = c["exit_taxonomy"]["classes"]
assert [k["status"] for k in classes] == statuses, "taxonomy statuses drifted from the schema enum"
assert [k["code"] for k in classes] == codes, "taxonomy codes drifted from the schema enum"
pairs = {b["if"]["properties"]["status"]["const"]: b["then"]["properties"]["exit_code"]["const"] for b in s["allOf"]}
assert list(pairs) == statuses, "the allOf branches are not one per status, in order"
assert pairs == {k["status"]: k["code"] for k in classes}, "status/exit_code pairing disagrees with the taxonomy"
kinds = s["$defs"]["triage_kind"]["enum"]
assert [k["kind"] for k in c["triage"]["kinds"]] == kinds, "triage kinds drifted from the schema enum"
assert all(set(k["typical_triage"]) <= set(kinds) for k in classes), "a class names a triage kind that does not exist"
dc = re.compile(s["$defs"]["diagnostic_code"]["pattern"])
assert all(dc.match(e["code"]) and e["code"].split(".")[0] in statuses for e in c["reserved_codes"]), "a reserved code is malformed or names no class"
assert all(e["code"].split(".")[1] == "clikit" for e in c["reserved_codes"]), "a reserved code is outside the reserved namespace"
assert c["record_schema"]["$id"] == s["$id"] and c["record_schema"]["version"] == s["version"], "contract points at another schema"
assert c["record_schema"]["schema_version"] == s["properties"]["schema_version"]["const"], "record MAJOR drifted"
seen = [json.loads(l)["status"] for l in open("schemas/clikit/examples/golden-results.jsonl")]
assert sorted(seen) == sorted(statuses), "the goldens do not cover every class exactly once"
lk  = json.load(open("schemas/logkit/logkit.contract.json"))
lks = json.load(open("schemas/logkit/log-record.schema.json"))
assert lk["version"].split("@")[1].split(".")[0] == str(c["logkit"]["record_schema_major"]), "logkit MAJOR moved past the pinned one"
assert lks["properties"]["schema_version"]["const"] == c["logkit"]["record_schema_major"], "pinned logkit record MAJOR disagrees with logkit"
levels = set(lks["$defs"]["level"]["enum"])
assert {k["log_level"] for k in classes} <= levels, "a class maps to a level logkit does not define"
fence = chr(96) * 3  # built from chr(96) so this snippet can live inside a fenced block
goldens = set(open("schemas/clikit/examples/golden-results.jsonl").read().splitlines())
blocks = [b for p in sorted(glob.glob("schemas/clikit/*.md")) for b in re.findall(fence + "json\n(.*?)\n" + fence, open(p).read(), re.S)]
assert blocks and all(json.dumps(json.loads(b), sort_keys=True, separators=(",", ":"), ensure_ascii=False) == b for b in blocks), "a documented example is missing or not canonical"
assert all(b in goldens for b in blocks if "status" in json.loads(b)), "a documented result record is not one of the goldens"
print("clikit contract invariants: ok")
'
```

The goldens are canonical bytes, checkable without a validator:

```sh
python3 -c '
import json
lines = open("schemas/clikit/examples/golden-results.jsonl").read().splitlines()
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
schema = json.load(open("schemas/clikit/result-record.schema.json"))
Draft202012Validator.check_schema(schema)
validator = Draft202012Validator(schema)
for n, line in enumerate(open("schemas/clikit/examples/golden-results.jsonl"), 1):
    for e in validator.iter_errors(json.loads(line)):
        raise SystemExit("line %d: %s: %s" % (n, e.json_path, e.message))
print("golden records: valid")
'
```

## Versioning

Released as `schemas/clikit/vX.Y.Z` — the tag prefix equals the module's path from the repository root, with no language segment (`tooling/version-guard` enforces it). Consumers pin a released tag, never this directory's `HEAD`.

`schema_version` on the record is the record contract's **MAJOR**. Any change to the set of records that validate — including adding an exit class or a triage kind — is a MAJOR, because downstream matches on both are exhaustive; adding an error **code** within an existing class is not a contract change at all. Full change classes and consumer expectations: `clikit-cli-contract.spec.md`, "Versioning and back-compat".
