# Divergences — declarative frontmatter schema vs `audit_helper/schema.py`

Load-bearing input to **M2.P2.T2** (frozen-fixture parity baseline): the declarative schema (`frontmatter-core.schema.json` + `frontmatter-psa-apm.pack.json`) is a **superset** of the stale `schema.py`. The deltas below are **reviewed, intended additions/changes** — M2.P2.T2 must treat them as approved deltas, not regressions, when capturing the current-Python verdicts as goldens. Everything NOT listed here is a faithful, behavior-preserving port (see the rule-coverage checklist in the task report).

Source of truth for the new values: workspace `CLAUDE.md` → Progressive Disclosure / Tagging Strategy. Source being externalized: `.claude/skills/workspace-audit/audit-helper/audit_helper/schema.py` (238 lines).

## D1 — `owner:` added as a singleton namespace (verdict-changing)

- **schema.py**: `SINGLE_VALUE_NAMESPACES = ("type", "status", "privacy")` — `owner` is NOT enforced at all.
- **Declarative**: `owner` added as a `singleton` namespace (`frontmatter-psa-apm.pack.json` → `namespaces`).
- **Authority**: CLAUDE.md — "`owner:` … exactly one … BOTH required on every file"; owner is the repo-split routing key.
- **Verdict impact**: any file lacking exactly one `owner:` tag now emits `MISSING_REQUIRED_TAG(owner)` (or `MULTIPLE_SINGLE_VALUE_TAGS(owner)`), where schema.py emitted nothing. This WILL flip verdicts on pre-owner files.
- **Determinism note**: `owner` is appended AFTER `privacy` in the namespaces array, so the emission order of the pre-existing singleton violations (type/status/privacy) is unchanged; owner's finding, when present, sorts last among singletons.

## D2 — `description_caps` added (new mechanism, code RESOLVED against live)

- **schema.py**: no description-length concept; no `description_caps`.
- **Declarative**: `description_caps` mechanism (core) + concrete caps `context:350 / skill:500 / agent:750` (pack). Violation code `DESCRIPTION_OVER_CAP`.
- **Authority**: CLAUDE.md frontmatter table — description cap by file class (context/workspace ≤350, Skills ≤500, Agents ≤750).
- **Verdict impact**: an over-cap `description` now emits `DESCRIPTION_OVER_CAP`; schema.py never checked length.
- **Provenance (corrected)**: the cap check is NOT in schema.py and is NOT the rule-tooling `description-yaml-scalar` rule (that rule handles scalar-quoting → `DESCRIPTION_INVALID_SCALAR`, a separate concern). The LENGTH cap is LIVE in the emitter `audit_helper/frontmatter.py:364-374`: `cap = DESCRIPTION_CAPS[file_class]; if isinstance(description, str) and len(description) > cap:` → `Violation("DESCRIPTION_OVER_CAP", "description", f"description is {len(description)} chars, over the {file_class.value} cap of {cap}")`.
- **RESOLVED**: the code string is aligned to the live emitter (`DESCRIPTION_OVER_CAP`, frontmatter.py:370) — no longer deferred to M2.P2.T4. The message template on `mechanisms.description_caps.message` is pinned to the live f-string (placeholders `{length}`/`{file_class}`/`{cap}`). Semantics confirmed: character count, keyed by derived `file_class`, fires strictly-greater. Residual: the core `meaning` says a class absent from the cap map is "uncapped"; the live emitter indexes the map directly (would `KeyError` on an absent class), so a pack MUST supply a cap for every derivable `file_class` (psa-apm@1 does: context/skill/agent all present). This is a robustness note for foreign packs, not a psa-apm@1 verdict divergence.

## D3 — `file_class` classification added (new mechanism, no code)

- **schema.py**: no file-class concept.
- **Declarative**: `file_class` mechanism (core) + path-glob rules (pack): `.claude/skills/*/SKILL.md` and `**/SKILL.md` → `skill`; `.claude/agents/*.md` → `agent`; else `default = context`.
- **Authority**: derived from CLAUDE.md's three description-cap classes (the only place a file is bucketed by kind for validation).
- **Verdict impact**: no violation emitted directly; it is the INPUT that selects the `description_caps` entry (D2). A misclassification would mis-cap a description, so the globs are ordered (anchored skill rule before the broad `**/SKILL.md`) and first-match-wins.
- **Classification logic (documented, NEW)**: evaluate `rules` in array order against the repo-root-relative POSIX path (fnmatch, `*` spans `/`); first match wins; no match → `default`.
- **Residual — agent glob is broader than the live predicate (documented, not guessed)**: the live `classify_file_class` (frontmatter.py:166-182) classifies AGENT only when `len(p.parts) == 3 and p.parts[0]=='.claude' and p.parts[1]=='agents' and p.suffix=='.md'` — i.e. EXACTLY `.claude/agents/<one-segment>.md`, no deeper nesting. The declarative glob `.claude/agents/*.md` under the stated `*`-spans-`/` fnmatch semantics ALSO matches nested paths like `.claude/agents/sub/x.md`, which live classifies as `context`. The `**/SKILL.md` skill rule faithfully reproduces live (`p.name == 'SKILL.md'`, any depth). Impact on the psa-apm corpus is ZERO — `.claude/agents/` holds one flat `.md` per subagent (CLAUDE.md convention), so no path triggers the difference and no golden (M2.P2.T2) exercises it. Not silently "fixed" here because the chosen single-`*` glob model cannot express "exactly depth 3" without contradicting the declared glob semantics. **Action for M2.P2.T1b/T3**: if a golden ever nests a `.md` under `.claude/agents/`, reconcile by matching live's exact-depth predicate (the live source is authoritative).

## D4 — per-field `authorship` (`machine_derivable | human_authored`) added (new mechanism)

- **schema.py**: `REQUIRED_FIELDS` is a flat tuple with no fix-tier metadata.
- **Declarative**: each `required_fields` entry carries `authorship`. Assignment: `updated` = `machine_derivable` (stampable at fix time); `name`/`description`/`id`/`tags`/`links` = `human_authored` (semantic/classification judgement no deterministic pass can author). `file_class` is a derived attribute → `machine_derivable`.
- **Authority**: build-plan M2.P1.T1 declaration list + M4.P3.T2 fix tiers (the boundary `navigator fix` reads from the schema, not hardcoded).
- **Verdict impact**: none on validation output; it drives the fix-tier split in M4.P3.T2 only.

## D5 — `MISSING_REQUIRED_FIELD` code string RESOLVED against live

- **schema.py**: declares the `REQUIRED_FIELDS` tuple (schema.py:60-62); the emitting code + literal code string live in the consumer `frontmatter.py`.
- **Declarative**: uses `MISSING_REQUIRED_FIELD` as the code string for the completeness check, with message template pinned to live.
- **RESOLVED**: confirmed against the live emitter `audit_helper/frontmatter.py:360-362`: `for field in REQUIRED_FIELDS: … Violation("MISSING_REQUIRED_FIELD", field, f"required field '{field}:' is missing")`. Code string, field (`{field}`), and message template all match live. `mechanisms.required_fields.message` is pinned to that f-string. No longer an assumption.

## Non-divergences (faithful ports — see report checklist)

The five tag-rule codes (`MISSING_REQUIRED_TAG`, `MULTIPLE_SINGLE_VALUE_TAGS`, `ORPHAN_NAMESPACE_TAG`, `REPORT_ONLY_TAG_MISUSED`, `INVALID_PERIOD_FORMAT`), their fields, message templates, cascade ORDER, the parent map (feature→product,suite; product→suite), report rules (required source+period; cadence-defaults; period regex), and the exempt taxonomy (filenames/dir-components/path-globs) are ported verbatim with source line citations. No behavior change there.
