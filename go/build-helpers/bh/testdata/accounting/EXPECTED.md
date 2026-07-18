# Fixture manifest — accounting golden transcripts

Ground-truth expected totals for `bh/testdata/accounting/`. All token counts are round numbers chosen so
sums and dollar costs are hand-verifiable. Rates are `anthropic-specifications.json` `pricing.list`
(USD per 1,000,000 tokens). Cost formula per model: `(input*rate.input + cache_write_5m*rate.cache_write_5m
+ cache_write_1h*rate.cache_write_1h + cache_read*rate.cache_read + output*rate.output) / 1e6`.

Rates used (from `anthropic-specifications.json` as of 2026-07-03):

| model | input | cache_write_5m | cache_write_1h | cache_read | output |
|---|---|---|---|---|---|
| claude-sonnet-5 | 3.00 | 3.75 | 6.00 | 0.30 | 15.00 |
| claude-opus-4-8 | 5.00 | 6.25 | 10.00 | 0.50 | 25.00 |

## Schema modeled (from live transcript inspection, not guessed)

Observed in `~/.claude/projects/<cwd-slug>/<session-id>.jsonl` (main) and
`<session-id>/subagents/agent-<id>.jsonl` (per-subagent), from live transcript inspection.

Usage-bearing line: `type: "assistant"`, usage at `message.usage`:
- `message.model` — exact string as issued. Main-transcript turns observed bare (`claude-opus-4-8`,
  `claude-sonnet-4-6`, `claude-sonnet-5` — matches `anthropic-specifications.json` model keys exactly).
  Subagent turns observed the same for sonnet/opus but haiku always carries a date suffix
  (`claude-haiku-4-5-20251001`) — a real-world quirk these fixtures deliberately do NOT model (see
  Assumptions below); a production parser must handle both bare and date-suffixed model strings.
- `message.usage.input_tokens`, `.cache_creation_input_tokens`, `.cache_read_input_tokens`, `.output_tokens`
- `message.usage.cache_creation.ephemeral_5m_input_tokens` / `.ephemeral_1h_input_tokens` — the 5m/1h split;
  `cache_creation_input_tokens` always equals their sum in real data (this invariant is preserved here).
- Subagent-only fields: `agentId` (matches the `subagents/agent-<agentId>.jsonl` filename) and
  `attributionAgent` (the dispatched subagent/role name, e.g. `claude-code-guide` in the observed sample).
- `isSidechain: true` on every subagent-transcript line; `false` on main-transcript lines.
- No field in the real schema carries an explicit task ID. The real correlator observed: the orchestrator's
  `Task`/`Agent` tool_use carries `input.description` + `input.prompt` (free text) and the subagent's own
  first `user` turn is that same prompt. This session's own dispatch prompt ("Implement ONE task
  (M2.P1.T1): ...") demonstrates the convention: **the task ID is literal text in the subagent's first user
  turn**, not a structured field. Fixtures model this: each subagent transcript's first line is a `user`
  turn whose text contains the literal task ID, exactly as a real dispatch would.
- Non-usage-bearing line types seen in real transcripts and NOT modeled here (irrelevant to accounting):
  `mode`, `system`, `file-history-snapshot`, `last-prompt`, `queue-operation`, `permission-mode`, `ai-title`,
  `attachment`, `user` (tool_result / non-dispatch content).

## Fixture inventory

```
testdata/accounting/
  orchestrator.jsonl                  — main transcript, 4 assistant turns, 2 models
  subagents/
    agent-agenta1.jsonl                — software-engineer, task M2.P1.T1, sonnet-5, 2 turns
    agent-agenta2.jsonl                — quality-reviewer,  task M2.P1.T1, opus-4-8, 1 turn
    agent-agenta3.jsonl                — test-engineer,     task M2.P1.T2, sonnet-5, 2 turns (concurrent w/ agenta4)
    agent-agenta4.jsonl                — software-engineer, task M2.P1.T3, opus-4-8, 2 turns (concurrent w/ agenta3)
  watermark/
    session.pass1.jsonl                — "first pass": orchestrator turns 1-2 only (sonnet-5 only)
    session.pass2.jsonl                — "appended": pass1 bytes + turns 3-4 appended (adds opus-4-8)
```

Concurrent fan-out scenario = `agent-agenta3.jsonl` + `agent-agenta4.jsonl`: different tasks (M2.P1.T2 vs
M2.P1.T3), assistant-turn timestamps interleaved across the two files (a3 at `18:20:15`/`18:20:30`, a4 at
`18:20:20`/`18:20:35`, user-dispatch turns at `18:20:00`/`18:20:05`). The interleaving demonstrates that
wall-clock order does NOT track agent/task boundaries, so a correct parser must key on file / `agentId` /
`attributionAgent` / dispatch-prompt task ID, never on timeline ordering.

**Adequacy caveat (T4) — this scenario does NOT force intra-file disambiguation.** a3 and a4 are separate
per-agent files, one task each, so a per-file attributor assigns them trivially and the interleaved
timestamps exercise nothing on that path. The genuinely hard attribution case — a *single* subagent
transcript that contains multiple task dispatches (an agent reused across tasks: several `user` turns with
different task IDs, whose subsequent assistant turns must be segmented by the preceding dispatch) — is NOT
modeled here. If T4's design must attribute within a shared/reused transcript, add that fixture before
relying on this set. See Assumptions & known gaps.

## Expected per-model totals

### orchestrator.jsonl

| model | input | cache_write_5m | cache_write_1h | cache_read | output |
|---|---|---|---|---|---|
| claude-sonnet-5 | 15000 | 4000 | 3000 | 3000 | 1500 |
| claude-opus-4-8 | 10000 | 2000 | 1000 | 4500 | 2500 |

Cost: sonnet-5 = (15000×3 + 4000×3.75 + 3000×6 + 3000×0.30 + 1500×15)/1e6 = (45000+15000+18000+900+22500)/1e6 = **$0.1014**
Cost: opus-4-8 = (10000×5 + 2000×6.25 + 1000×10 + 4500×0.50 + 2500×25)/1e6 = (50000+12500+10000+2250+62500)/1e6 = **$0.13725**

### Per-subagent totals (task attribution — M2.P1.T4)

| file | task | role | model | input | c5m | c1h | cread | output | cost |
|---|---|---|---|---|---|---|---|---|---|
| agent-agenta1.jsonl | M2.P1.T1 | software-engineer | sonnet-5 | 4000 | 1000 | 500 | 500 | 500 | $0.0264 |
| agent-agenta2.jsonl | M2.P1.T1 | quality-reviewer | opus-4-8 | 2000 | 500 | 0 | 1000 | 400 | $0.023625 |
| agent-agenta3.jsonl | M2.P1.T2 | test-engineer | sonnet-5 | 4000 | 800 | 400 | 300 | 400 | $0.02349 |
| agent-agenta4.jsonl | M2.P1.T3 | software-engineer | opus-4-8 | 3000 | 600 | 200 | 400 | 300 | $0.02845 |

Task M2.P1.T1 combined (agenta1 + agenta2, two models, two roles): sonnet-5 as above + opus-4-8 as above;
combined cost = 0.0264 + 0.023625 = **$0.050025**.

The per-task attribution pass (M2.P1.T4) uses these existing fixtures for the fan-out and multi-transcript
cases (no fixture change here): agenta3→M2.P1.T2 (**$0.02349**), agenta4→M2.P1.T3 (**$0.02845**), and
agenta1+agenta2→M2.P1.T1 (**$0.050025**, summed across two transcripts/models/roles).

### Attribution-specific fixtures (M2.P1.T4) — `testdata/attribution/`

A SEPARATE fixture tree (NOT under `testdata/accounting/subagents/`, so the whole-session grand-total
counts above are never disturbed — the accounting glob still finds exactly its 4 subagents). It exercises
the two cases the fan-out set cannot: the shared-prefix exact-match guard and the unmappable fallback.

```
testdata/attribution/
  orchestrator.jsonl                  — minimal main transcript (opus-4-8, 1 turn); NOT attributed
  subagents/
    agent-b3.jsonl                     — task M2.P1.T3,  sonnet-5, 1 turn (summary also names M2.P1.T31)
    agent-b31.jsonl                    — task M2.P1.T31, opus-4-8, 1 turn (summary also names M2.P1.T3)
    agent-bx.jsonl                     — UNMAPPABLE: first user turn carries NO task ID, sonnet-5, 1 turn
```

| file | task | model | input | c5m | c1h | cread | output | cost |
|---|---|---|---|---|---|---|---|---|
| agent-b3.jsonl | M2.P1.T3 | sonnet-5 | 2000 | 400 | 200 | 100 | 200 | $0.01173 |
| agent-b31.jsonl | M2.P1.T31 | opus-4-8 | 1000 | 200 | 100 | 200 | 100 | $0.00985 |
| agent-bx.jsonl | (none) | sonnet-5 | 500 | 100 | 0 | 0 | 100 | $0.003375 |

Cost: b3 sonnet-5 = (2000×3 + 400×3.75 + 200×6 + 100×0.30 + 200×15)/1e6 = (6000+1500+1200+30+3000)/1e6 = **$0.01173**
Cost: b31 opus-4-8 = (1000×5 + 200×6.25 + 100×10 + 200×0.50 + 100×25)/1e6 = (5000+1250+1000+100+2500)/1e6 = **$0.00985**
Cost: bx sonnet-5 = (500×3 + 100×3.75 + 0 + 0 + 100×15)/1e6 = (1500+375+1500)/1e6 = **$0.003375**

- **Shared-prefix guard.** The leading `Task <id>` token of each of b3/b31 is the spawning task; each summary
  ALSO names the sibling ID, so the leftmost-match + greedy-digit extractor is exercised (b31 stays whole,
  never truncates to T3). With known set `{M2.P1.T3, M2.P1.T31}`, b3→T3 ($0.01173) and b31→T31 ($0.00985),
  each carrying ONLY its own model buckets — no cross-attribution.
- **Not-in-known-set is unmappable, not force-matched.** With known set `{M2.P1.T3}` only, b31's extracted
  `M2.P1.T31` matches no entry (exact equality — it does NOT collide with the shorter `M2.P1.T3`), so its
  $0.00985 is pooled, never leaked into T3.
- **Unmappable even-split.** bx has no task ID → its $0.003375 lands in the flagged `batch-even-split` pool,
  split evenly across the known tasks (with 2 known: per-task share **$0.0016875**), surfaced in
  `unmappable[]` with reason `no task-id in first user turn`. Conservation: measured + pooled equals the sum
  of every transcript's true cost — nothing dropped, nothing double-counted.

### Grand total (orchestrator + all 4 subagent transcripts) — whole-run true cost

| model | input | c5m | c1h | cread | output |
|---|---|---|---|---|---|
| claude-sonnet-5 | 23000 | 5800 | 3900 | 3800 | 2400 |
| claude-opus-4-8 | 15000 | 3100 | 1200 | 5900 | 3200 |

Cost: sonnet-5 = (23000×3 + 5800×3.75 + 3900×6 + 3800×0.30 + 2400×15)/1e6 = (69000+21750+23400+1140+36000)/1e6 = **$0.1513**... exact: **$0.15129**
Cost: opus-4-8 = (15000×5 + 3100×6.25 + 1200×10 + 5900×0.50 + 3200×25)/1e6 = (75000+19375+12000+2950+80000)/1e6 = **$0.189325**
Grand total cost, both models: $0.15129 + $0.189325 = **$0.340615**

### Watermark / rotation scenario (M2.P1.T2)

`watermark/session.pass1.jsonl` is byte-length 1946 and is an **exact byte-prefix** of
`watermark/session.pass2.jsonl` (byte-length 3924) — confirmed via `cmp`/`diff` at authoring time.
This is the "first pass, then appended" shape: a watermark parser that records offset 1946 after pass1
and resumes reading pass2 from that offset sees only the 1978 appended bytes (orchestrator turns 3-4,
opus-4-8) — never re-reads or re-sums turns 1-2.

- Parse pass1 fresh → sonnet-5 only: input=15000, c5m=4000, c1h=3000, cread=3000, output=1500 (cost $0.1014)
- Parse pass2 fresh (no watermark) → sonnet-5 (same as above) + opus-4-8: input=10000, c5m=2000, c1h=1000,
  cread=4500, output=2500 (cost $0.13725) — combined = the `orchestrator.jsonl` totals above (pass2 IS
  orchestrator.jsonl in full).
- Parse pass2 from watermark offset 1946 (incremental) → opus-4-8 ONLY (the appended turns): same numbers
  as the opus-4-8 row above. Idempotency check: incremental-sum(pass1) + incremental-sum(pass2 from offset)
  == fresh-sum(pass2) for every bucket — this is the assertion the watermark test should make.

## Independent-sum confirmation

Summed programmatically at authoring time by replaying the exact `message.usage` extraction path
(`message.model`, `usage.input_tokens`, `usage.cache_creation.ephemeral_5m_input_tokens`,
`usage.cache_creation.ephemeral_1h_input_tokens`, `usage.cache_read_input_tokens`, `usage.output_tokens`)
against every fixture file. Output matched every table above exactly (orchestrator, each subagent, grand
total, both watermark passes) — no discrepancy on first run.

## Assumptions & known gaps

- Model strings: fixtures use only bare `claude-sonnet-5` / `claude-opus-4-8` (matching
  `anthropic-specifications.json` keys exactly) to keep rate lookup trivial. Real subagent transcripts show
  haiku with a date-suffixed model string (`claude-haiku-4-5-20251001`) that does NOT match its bare spec
  key (`claude-haiku-4-5`) — a real parser needs prefix/suffix-tolerant model-key matching. Not exercised by
  these fixtures; flag for a follow-up fixture or unit test once the parser's model-matching rule is built.
- **Task-ID correlation contract — DECIDED (M2.P1.T3).** `batch-engine.workflow.js` returns
  `per_task_transcript_map: { task_id, label, run_id }[]`, one entry per batched task. Investigated:
  the in-script `workflow()` hook exposes NO nested-run identifier to the caller (no `wf_*` id
  surfaces through its return) — confirmed by reading both `batch-engine.workflow.js` and
  `build-engine.workflow.js`, and no other workspace doc claims one either — so `run_id` is always
  `null` today. The correlator is `task_id` (== `label`), a verbatim, regex/exact-matchable token
  that build-engine already embeds two ways in every nested `agent()` call for that task, unchanged
  by this task: (1) `opts.label` prefix `<phase>:<task_id>[#<round>] <model>/<effort>`; (2) the
  literal first line of every dispatch prompt, `Task <task_id> — <summary>` (`taskBrief`), which
  becomes the subagent transcript's first `user` turn per the convention this manifest already
  documents above. **T4 implements:** for each `agent-*.jsonl`, extract `task_id` from its first
  `user` turn via the `[A-Z][0-9]+\.P[0-9]+\.T[0-9]+` regex (primary — always present) or the
  `opts.label` prefix if the harness later surfaces labels into transcripts (secondary,
  unconfirmed); attribute all usage in that file to the matching `per_task_transcript_map` entry.
  **Extraction and match are both anchored, to defend the shared-prefix collision (`M2.P1.T3` vs
  `M2.P1.T31`):** (a) EXTRACTION takes the FIRST regex match in the turn — `taskBrief` always begins
  with `Task <task_id>`, so the leading match is the spawning task even if `<summary>` text later
  embeds another task id; the regex's greedy `\.T[0-9]+` captures the full digit run, so `T31` never
  truncates to `T3`. (b) MATCH is EXACT full-string equality between the extracted token and
  `per_task_transcript_map[].task_id` — never substring/`includes`/`startsWith`, which would let
  `M2.P1.T31` collide with entry `M2.P1.T3`. Both `task_id` and `label` in the map are the bare id,
  so equality holds against either field.
  No `agentId`/`attributionAgent` join needed for task attribution (those identify role, not task).
  **Fixture status: no extension needed for this contract** — every existing subagent fixture
  already carries the literal task ID as the first line of its first `user` turn (the pre-existing
  documented convention above); T4's regex-extraction unit test can run directly against this set.
  The SEPARATE gap noted below (reused-transcript segmentation) is real but orthogonal — it is about
  multiple dispatches sharing one transcript file, not about the correlator format decided here.
- Timestamps are synthetic (`2026-07-03T18:MM:SS.000Z`), not from a real run — only their relative order/
  overlap matters for the concurrent-fan-out scenario.
- Orchestrator→subagent dispatch linkage NOT modeled (T4 dependency): `orchestrator.jsonl` is four plain
  `assistant` text turns — it carries no `Task`/`Agent` `tool_use` block with `input.description`/
  `input.prompt`. The real, more-robust correlator anchors on that dispatch record (orchestrator tool_use →
  `agentId`); these fixtures support only the *secondary* signal (each subagent's own first `user` turn). If
  T4 correlates via the orchestrator dispatch record rather than the subagent-side prompt, add dispatch
  tool_use lines to the orchestrator fixture and extend this manifest.
- Nested `cache_creation` split-object robustness NOT modeled (T2 dependency): every fixture turn carries a
  present `cache_creation` object with both `ephemeral_5m/1h` sub-fields. The current `usage.go` parser reads
  only the flat `cache_creation_input_tokens` — T2's per-rate 5m/1h split parsing is NET-NEW code that will
  newly dereference `cache_creation.*`. No fixture exercises an assistant turn where the nested object is
  ABSENT (real older transcripts) — add a unit/fixture case with the object missing to prove T2's nil-guard.
  Already covered at unit level in `bh/usage_test.go` (do not re-model here): unknown-field silent-skip,
  `cache_creation_input_tokens:0`, top-level vs `message`-nested usage, and non-usage line skip.
- Trimmed non-load-bearing real fields (`thinking` content, `signature`, `iterations`, `server_tool_use`,
  `speed`, `inference_geo`) — absent in these fixtures since the parser reads only `message.model` and the
  five usage buckets; adding them back would not change any test assertion.
