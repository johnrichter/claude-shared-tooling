---
name: Nested-Transcript Discovery Fixtures — Expected Totals
description: "golden transcript fixture manifest for nested-workflow discovery — real on-disk depth (session/subagents/workflows/wf_*/agent-*.jsonl), the O-isolation + per-task attribution numbers, and the assertions that fail on pre-change SubagentGlobs"
---

# Fixture manifest — nested-transcript discovery golden fixtures

Ground-truth expected totals for `bh/testdata/nested/`. Separate tree from `testdata/accounting/` and
`testdata/attribution/` — those fixtures and their manifest are undisturbed. Rates are
`anthropic-specifications.json` `pricing.list` (USD per 1,000,000 tokens), same formula and rate table
as `testdata/accounting/EXPECTED.md`:

| model | input | cache_write_5m | cache_write_1h | cache_read | output |
|---|---|---|---|---|---|
| claude-sonnet-5 | 3.00 | 3.75 | 6.00 | 0.30 | 15.00 |
| claude-opus-4-8 | 5.00 | 6.25 | 10.00 | 0.50 | 25.00 |

## Fixture inventory — real nesting depth

```
testdata/nested/
  orchestrator.jsonl                                        — MAIN, sonnet-5, 1 turn; the sole O transcript
  orchestrator/
    subagents/
      agent-direct.jsonl                                    — DEPTH-1 direct subagent, task M13.P2.T1, opus-4-8, 1 turn (the today-covered control)
      workflows/
        wf_batch/journal.jsonl                              — workflow run journal; NO usage lines (type started/result only) — must never be summed/attributed
        wf_eng1/agent-e1.jsonl                               — DEPTH-3 nested build-engine agent, task M13.P2.T3,  sonnet-5,  2 turns (interleaved w/ wf_eng2)
        wf_eng2/agent-e2.jsonl                               — DEPTH-3 nested build-engine agent, task M13.P2.T31, opus-4-8, 2 turns (interleaved w/ wf_eng1)
```

`orchestrator/` (not `orchestrator.jsonl`'s dir sibling `subagents/`, which is empty here) is the LIVE
layout root: `<dir>/<stem>/subagents/...` where `stem` = the main transcript's basename minus extension
(`orchestrator`) — the exact convention `DiscoverSubagentTranscripts`/`SubagentGlobs` both key on. The
two depth-3 agents sit at `orchestrator/subagents/workflows/wf_*/agent-*.jsonl` — three path segments
below the main transcript's directory, the depth today's fixed two-pattern `SubagentGlobs` provably
cannot reach (`filepath.Glob` has no `**` recursion). `wf_batch` is the batch-engine's own run
(spawning `wf_eng1`/`wf_eng2`, each a nested build-engine run for one task); the runtime flattens all
three into the SAME `workflows/` dir as siblings — modeled
here exactly that way, not with `wf_eng1`/`wf_eng2` nested inside `wf_batch`.

**Shared-prefix ids + interleaving.** Known task set `{M13.P2.T1, M13.P2.T3, M13.P2.T31}`. `agent-e1`'s
first user turn leads with `Task M13.P2.T3` and also names `M13.P2.T31`; `agent-e2`'s leads with
`Task M13.P2.T31` and also names `M13.P2.T3` — exercising the leftmost-match + greedy-digit guard at
real nesting depth (not just in the flat `testdata/attribution/` fixtures). Assistant-turn timestamps
interleave across the two files: e1@`:00`(dispatch)/`:15`/`:25`, e2@`:05`(dispatch)/`:20`/`:35` — order
by wall clock is dispatch-e1, dispatch-e2, turn1-e1, turn1-e2, turn2-e1, turn2-e2, proving attribution
keys on file/task-id, never timeline order (mirrors `testdata/accounting/EXPECTED.md`'s agenta3/agenta4
scenario, reproduced at the real depth-3 nested-workflow location).

## Expected per-transcript totals

| file | task | model | input | c5m | c1h | cread | output | turns | cost |
|---|---|---|---|---|---|---|---|---|---|
| orchestrator.jsonl | (main, not a task) | sonnet-5 | 6000 | 1200 | 600 | 900 | 300 | 1 | $0.03087 |
| agent-direct.jsonl | M13.P2.T1 | opus-4-8 | 4000 | 800 | 400 | 600 | 200 | 1 | $0.0343 |
| agent-e1.jsonl | M13.P2.T3 | sonnet-5 | 3000 | 600 | 300 | 450 | 150 | 2 | $0.015435 |
| agent-e2.jsonl | M13.P2.T31 | opus-4-8 | 2000 | 400 | 200 | 300 | 100 | 2 | $0.01715 |

Cost: orchestrator sonnet-5 = (6000×3 + 1200×3.75 + 600×6 + 900×0.30 + 300×15)/1e6 = (18000+4500+3600+270+4500)/1e6 = **$0.03087**
Cost: agent-direct opus-4-8 = (4000×5 + 800×6.25 + 400×10 + 600×0.50 + 200×25)/1e6 = (20000+5000+4000+300+5000)/1e6 = **$0.0343**
Cost: agent-e1 sonnet-5 = (3000×3 + 600×3.75 + 300×6 + 450×0.30 + 150×15)/1e6 = (9000+2250+1800+135+2250)/1e6 = **$0.015435**
Cost: agent-e2 opus-4-8 = (2000×5 + 400×6.25 + 200×10 + 300×0.50 + 100×25)/1e6 = (10000+2500+2000+150+2500)/1e6 = **$0.01715**

`wf_batch/journal.jsonl` contributes $0 to every bucket/cost/attribution — its two lines (`started`,
`result`) carry no `usage` object at any level, so `foldLine`/`ParseTranscriptForAttribution` skip both
lines outright (the same no-usage-object skip path every other non-billable line type uses).

## Assertion 1 — discovery completeness

`DiscoverSubagentTranscripts(testdata/nested/orchestrator.jsonl)` must return exactly 3 paths:
`agent-direct.jsonl`, `agent-e1.jsonl`, `agent-e2.jsonl` — never `journal.jsonl`. The legacy
`SubagentGlobs` two-fixed-depth-glob approach finds only 1 (`agent-direct.jsonl`, the depth-1 file both
its patterns can reach) — pinned as a regression: the new seam must find strictly more
than the old two glob patterns ever could, on this exact fixture.

## Assertion 2 — O-isolation

`Accounting.PriceFile(mainFileID)` after folding ALL FOUR discovered transcripts (main + 3 subagents)
must equal ONLY `orchestrator.jsonl`'s own cost, **$0.03087** — unaffected by how many subagents were
discovered or folded alongside it (PriceFile prices only the main file's own ledger entry, by
construction; see `accounting.go`). The grand total across all four files must equal
**$0.03087 + $0.0343 + $0.015435 + $0.01715 = $0.097755** — reachable ONLY when discovery finds all 3
subagents; pre-change (2 files: main + agent-direct only) the grand total is short by
`$0.015435 + $0.01715 = $0.032585` (the two nested transcripts' cost, never read at all, so it inflates
neither O nor the discovered total — it is simply absent, which is the completeness failure this
assertion pins).

## Assertion 3 — per-task attribution at real nesting depth

`Attribute` over the same 3 discovered subagent sources, known set
`{M13.P2.T1, M13.P2.T3, M13.P2.T31}`, must yield:

- `M13.P2.T1` → `agent-direct.jsonl` only, opus-4-8, **$0.0343**
- `M13.P2.T3` → `agent-e1.jsonl` only, sonnet-5, **$0.015435** (never leaking `M13.P2.T31`'s opus-4-8 buckets)
- `M13.P2.T31` → `agent-e2.jsonl` only, opus-4-8, **$0.01715** (never truncated to `M13.P2.T3`)
- No unmappable transcripts, no even-split pool — all 3 discovered subagents map cleanly.

Pre-change, `agent-e1.jsonl`/`agent-e2.jsonl` are never discovered/opened at all, so `M13.P2.T3` and
`M13.P2.T31` receive zero measured cost each — the exact "nested subagent cost degrades to even-split"
failure this discovery seam closes.

## Assertion 4 — journal excluded

`wf_batch/journal.jsonl` never appears in `DiscoverSubagentTranscripts`'s output, never contributes a
ledger entry, and never appears in `Attribute`'s `Unmappable` list (it is excluded by name before
either consumer ever sees it — the walk only matches `agent-*.jsonl`).

## The agreeing key (AC1)

Both the O-isolation source list (`TranscriptSource.FileID`) and the attribution source list
(`AttribSource.FileID`) are built from the SAME `DiscoverSubagentTranscripts` return value in the tests
below — byte-identical path strings, not independently re-derived. This is the seam invariant the
discovery contract requires.

## Independent-sum confirmation

Summed by hand against the same `message.usage` extraction path as `testdata/accounting/EXPECTED.md`
(`message.model`, `usage.input_tokens`, `usage.cache_creation.ephemeral_5m_input_tokens`,
`usage.cache_creation.ephemeral_1h_input_tokens`, `usage.cache_read_input_tokens`,
`usage.output_tokens`) against every fixture file above — no discrepancy.

## Directory naming

The actual `SubagentGlobs`/`DiscoverSubagentTranscripts` contract keys the live root on `<stem>/subagents`
where `stem` is the main transcript's own basename minus extension — so this fixture names that
directory `orchestrator/` (matching `orchestrator.jsonl`'s stem) rather than `session/`, to exercise the
real production key derivation rather than a hand-picked dir name.
