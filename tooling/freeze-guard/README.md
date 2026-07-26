# freeze-guard — SC-FREEZE: deny writes to frozen plugin/corpus homes

Pre-merge guardrail that fails CI if any diff touches paths under datadog-docs-agent or datadog-code-agent plugin/corpus homes. Passes for writes to Toolbelt build homes and any other writable locations.

Frozen homes are frozen (read-only via CI) after an initial release — their contents are vendored into downstream products (marketplace plugins, etc.) and must never mutate in-place to avoid divergence and supply-chain risk.

## What it guards

The guard resolves the changed-file surface, then checks each path against a **single source of record** (`frozen-homes.json`): frozen if it equals a frozen home or is under one (prefix match + path-separator boundary). Any match → exit 1.

Surface resolution:
- **`--base <ref>` (CI/pre-merge):** the committed diff `<ref>..HEAD` — the PR's changed files. This is the mode CI uses; the base is the PR base SHA. A clean CI checkout has no uncommitted state, so without a base the guard would see nothing.
- **no `--base` (local):** the uncommitted working tree only (staged, unstaged, untracked). Both modes also union the working tree so a dirty checkout is never missed.

**Frozen homes** (`frozen-homes.json`):
- `plugin-homes/datadog-docs-agent` — plugin home and all descendants
- `plugin-homes/datadog-code-agent` — plugin home and all descendants
- `corpus/datadog-docs-agent` — corpus and all descendants
- `corpus/datadog-code-agent` — corpus and all descendants

**Exempt** (allowed to write):
- Toolbelt build homes
- Any directory not matching a frozen pattern

## Usage

```sh
python3 tooling/freeze-guard/check.py --base <ref>   # diff <ref>..HEAD (CI/pre-merge)
python3 tooling/freeze-guard/check.py                # working-tree mode (local)
python3 tooling/freeze-guard/check.py --list         # list frozen paths
```

Exit codes: `0` no violations; `1` at least one file touches a frozen home; `2` harness failure (e.g., git not available).

## How to update frozen paths

Edit `frozen-homes.json` and commit. The guard reads it on every CI run, so the change takes effect immediately.

**Caution:** Adding a new frozen path will fail all in-flight PRs that touch it — coordinate with the team.

## Single source of record

The frozen-home path list is **never** duplicated elsewhere (no hardcoded path lists in the check script, no environment-based toggles). The script reads `frozen-homes.json` every run and holds no cached state.

## Coverage

Probes: a planted write into a frozen home → exit 1; a write elsewhere → exit 0.

## Layout

| file | role |
| --- | --- |
| `check.py` | CLI: the guard |
| `frozen-homes.json` | Single source of record for frozen paths (re-read per run; spec-drift covered by `tests/test_freeze_guard.py`) |
| `README.md` | This file |
