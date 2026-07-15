package bh

import (
	"strings"
	"testing"
)

// These golden fixtures are synthetic Claude Code session transcripts (JSONL) covering the
// three usage shapes ParseTranscriptUsage recognizes:
//   - topLevelUsageFixture:  `usage` sits directly on the line object (no `message` wrapper).
//   - messageUsageFixture:   `usage` is nested under `message` — the common case.
//   - subagentUsageFixture:  a `message.usage` turn additionally tagged with
//     `parent_tool_use_id`, i.e. a subagent's own assistant turn, which must be counted toward
//     the whole-session true total exactly like a top-level turn.
//
// usageRaw's field names (input_tokens, cache_creation_input_tokens, cache_read_input_tokens,
// output_tokens) mirror the Anthropic Messages API usage shape that Claude Code embeds
// verbatim in the transcript. That shape is INTERNAL to Claude Code and not owned by this
// workspace — see rule:tooling:shared-directory-enumerator-drift's sibling failure mode: a
// silent upstream rename is exactly the kind of drift a static rule cannot catch, only a fixture
// with known expected totals can. TestParseTranscriptUsage_UpstreamFieldRenameIsCaught below
// demonstrates that: if any of these field names were renamed upstream, json.Unmarshal would
// silently leave usageRaw zeroed (Go ignores unknown JSON keys by default), ParseTranscriptUsage
// would tolerate it via the "no usage object" skip path, and the true-token totals below would
// silently drop to a wrong (lower) number instead of failing loudly. Asserting exact totals here
// is what turns that silent zeroing into a hard test failure.

const topLevelUsageFixture = `{"type":"assistant","usage":{"input_tokens":100,"cache_creation_input_tokens":10,"cache_read_input_tokens":5,"output_tokens":50}}
`

const messageUsageFixture = `{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":20,"output_tokens":80}}}
`

const subagentUsageFixture = `{"type":"assistant","parent_tool_use_id":"toolu_01Subagent","message":{"role":"assistant","usage":{"input_tokens":30,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":15}}}
`

// mixedSessionFixture combines all three shapes into one transcript, plus a blank line and a
// line with no usage object at all, both of which must be skipped without affecting the total.
const mixedSessionFixture = topLevelUsageFixture + `
` + messageUsageFixture + `{"type":"user","message":{"role":"user","content":"no usage here"}}
` + subagentUsageFixture

// splitCacheFixture carries the nested cache_creation 5m/1h split newer Claude Code transcripts
// emit: ephemeral_5m_input_tokens + ephemeral_1h_input_tokens sum to cache_creation_input_tokens.
// The flat parser recombines them into CacheCreationTokens; the per-model parser (accounting_test)
// keeps them apart for the differentiated 5m/1h cache-write rates. Keeping this fixture current with
// the real transcript shape is what rule:tooling:usage-parser-fixture-current requires.
const splitCacheFixture = `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":300,"cache_creation_input_tokens":900,"cache_read_input_tokens":40,"output_tokens":120,"cache_creation":{"ephemeral_5m_input_tokens":600,"ephemeral_1h_input_tokens":300}}}}
`

// absentCacheFixture is an older-shape turn with NO nested cache_creation object — only the flat
// cache_creation_input_tokens. The parser must not dereference the absent nested object and must
// still count the flat total (the T2 nil-guard).
const absentCacheFixture = `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":300,"cache_creation_input_tokens":700,"cache_read_input_tokens":40,"output_tokens":120}}}
`

// TestParseTranscriptUsage_NestedCacheCreationSplit asserts the flat totals recombine the nested
// 5m/1h split: CacheCreationTokens must equal ephemeral_5m + ephemeral_1h (600+300 = 900).
func TestParseTranscriptUsage_NestedCacheCreationSplit(t *testing.T) {
	u, err := ParseTranscriptUsage(strings.NewReader(splitCacheFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.CacheCreationTokens != 900 {
		t.Fatalf("CacheCreationTokens = %d, want 900 (600 5m + 300 1h from the nested split)", u.CacheCreationTokens)
	}
	if u.InputTokens != 300 || u.CacheReadTokens != 40 || u.OutputTokens != 120 {
		t.Fatalf("got %+v, want input=300 cacheRead=40 output=120", u)
	}
}

// TestParseTranscriptUsage_AbsentNestedCacheCreation asserts an older-shape turn with no nested
// cache_creation object neither crashes nor loses the flat cache_creation_input_tokens total.
func TestParseTranscriptUsage_AbsentNestedCacheCreation(t *testing.T) {
	u, err := ParseTranscriptUsage(strings.NewReader(absentCacheFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.CacheCreationTokens != 700 {
		t.Fatalf("CacheCreationTokens = %d, want 700 (flat cache_creation_input_tokens honored when nested object absent)", u.CacheCreationTokens)
	}
}

func TestParseTranscriptUsage_TopLevel(t *testing.T) {
	u, err := ParseTranscriptUsage(strings.NewReader(topLevelUsageFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.InputTokens != 100 || u.CacheCreationTokens != 10 || u.CacheReadTokens != 5 || u.OutputTokens != 50 {
		t.Fatalf("got %+v, want input=100 cacheCreate=10 cacheRead=5 output=50", u)
	}
	if u.TotalTokens != 165 {
		t.Fatalf("TotalTokens = %d, want 165", u.TotalTokens)
	}
	if u.Turns != 1 {
		t.Fatalf("Turns = %d, want 1", u.Turns)
	}
}

func TestParseTranscriptUsage_MessageUsage(t *testing.T) {
	u, err := ParseTranscriptUsage(strings.NewReader(messageUsageFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.InputTokens != 200 || u.CacheCreationTokens != 0 || u.CacheReadTokens != 20 || u.OutputTokens != 80 {
		t.Fatalf("got %+v, want input=200 cacheCreate=0 cacheRead=20 output=80", u)
	}
	if u.TotalTokens != 300 {
		t.Fatalf("TotalTokens = %d, want 300", u.TotalTokens)
	}
	if u.Turns != 1 {
		t.Fatalf("Turns = %d, want 1", u.Turns)
	}
}

func TestParseTranscriptUsage_SubagentTurn(t *testing.T) {
	u, err := ParseTranscriptUsage(strings.NewReader(subagentUsageFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.InputTokens != 30 || u.CacheCreationTokens != 0 || u.CacheReadTokens != 0 || u.OutputTokens != 15 {
		t.Fatalf("got %+v, want input=30 cacheCreate=0 cacheRead=0 output=15", u)
	}
	if u.TotalTokens != 45 {
		t.Fatalf("TotalTokens = %d, want 45 — a subagent turn (parent_tool_use_id set) must count toward the whole-session true total, not be dropped", u.TotalTokens)
	}
	if u.Turns != 1 {
		t.Fatalf("Turns = %d, want 1", u.Turns)
	}
}

// TestParseTranscriptUsage_MixedSession is the primary golden-fixture regression test: a single
// transcript exercising all three shapes plus a blank line and a no-usage line, asserting the
// combined whole-session true total. This is the shape execution.md's true-token line depends on.
func TestParseTranscriptUsage_MixedSession(t *testing.T) {
	u, err := ParseTranscriptUsage(strings.NewReader(mixedSessionFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantInput := int64(100 + 200 + 30)
	wantCacheCreate := int64(10 + 0 + 0)
	wantCacheRead := int64(5 + 20 + 0)
	wantOutput := int64(50 + 80 + 15)
	if u.InputTokens != wantInput || u.CacheCreationTokens != wantCacheCreate || u.CacheReadTokens != wantCacheRead || u.OutputTokens != wantOutput {
		t.Fatalf("got %+v, want input=%d cacheCreate=%d cacheRead=%d output=%d", u, wantInput, wantCacheCreate, wantCacheRead, wantOutput)
	}
	wantTotal := wantInput + wantCacheCreate + wantCacheRead + wantOutput
	if u.TotalTokens != wantTotal {
		t.Fatalf("TotalTokens = %d, want %d", u.TotalTokens, wantTotal)
	}
	if u.Turns != 3 {
		t.Fatalf("Turns = %d, want 3 (top-level + message.usage + subagent)", u.Turns)
	}
}

// TestParseTranscriptUsage_UnknownFieldsAreSilentlySkipped documents the exact failure mode B15
// remedies: usageRaw's json tags are best-effort by design (see usage.go's doc comment), so an
// upstream field rename does NOT surface as a parse error — json.Unmarshal succeeds with the
// renamed field simply absent from usageRaw, leaving it zeroed. This test locks in a fixture
// using the CURRENT field name (input_tokens) alongside one using a hypothetical renamed field,
// proving that only the correctly-named field contributes to the total: a silent rename would
// zero out the renamed field's contribution rather than failing loudly. Any golden fixture above
// that asserts an exact non-zero total for a specific field is what turns a real upstream rename
// into a hard test failure instead of this silent-skip behavior.
func TestParseTranscriptUsage_UnknownFieldsAreSilentlySkipped(t *testing.T) {
	renamed := `{"type":"assistant","usage":{"input_tokens_v2":100,"cache_creation_input_tokens":10,"cache_read_input_tokens":5,"output_tokens":50}}
`
	u, err := ParseTranscriptUsage(strings.NewReader(renamed))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.InputTokens != 0 {
		t.Fatalf("InputTokens = %d, want 0 — this documents that a renamed field is silently dropped rather than erroring; it is exactly the failure mode the fixtures above with exact non-zero totals are designed to catch", u.InputTokens)
	}
}
