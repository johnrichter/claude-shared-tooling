package bh

import (
	"io"
)

// usageRaw mirrors the `usage` object Claude Code writes on each assistant turn in the session
// transcript JSONL. Field names track the Anthropic Messages API usage shape. This format is
// INTERNAL to Claude Code and may change between releases — parsing is best-effort and tolerates
// missing fields/lines rather than failing the whole parse.
//
// CacheCreation is the nested 5m/1h split Claude Code emits on newer transcripts; when present its
// two sub-fields sum to CacheCreationTokens (the flat total). Older transcripts omit the nested
// object entirely — buckets() falls back to the flat CacheCreationTokens so the 5m/1h split is
// never dereferenced on a nil object.
type usageRaw struct {
	InputTokens         int64 `json:"input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreation       *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// buckets splits one raw usage object into the five priced buckets. Cache-write tokens are split
// 5m/1h from the nested cache_creation object when it is present; when it is ABSENT (older
// transcripts), the flat cache_creation_input_tokens is assigned wholesale to the 5m bucket (the
// standard cache TTL) so the flat total is preserved and no nil dereference occurs.
func (u usageRaw) buckets() ModelBuckets {
	b := ModelBuckets{
		Input:     u.InputTokens,
		CacheRead: u.CacheReadTokens,
		Output:    u.OutputTokens,
		Turns:     1,
	}
	if u.CacheCreation != nil {
		b.CacheWrite5m = u.CacheCreation.Ephemeral5m
		b.CacheWrite1h = u.CacheCreation.Ephemeral1h
	} else {
		b.CacheWrite5m = u.CacheCreationTokens
	}
	return b
}

// lineProbe finds the usage object and its model on a transcript line whether they sit at the top
// level or (the common case) nested under `message`. Subagent turns carry the same shape and are
// counted too — the goal is the whole-session true total.
type lineProbe struct {
	Model   string    `json:"model"`
	Usage   *usageRaw `json:"usage"`
	Message *struct {
		Model string    `json:"model"`
		Usage *usageRaw `json:"usage"`
	} `json:"message"`
}

// resolve returns the turn's model string and usage object, preferring the message-nested shape's
// values. ok is false for a line with no usage object (skipped by the parser).
func (lp lineProbe) resolve() (model string, u *usageRaw, ok bool) {
	if lp.Usage != nil {
		return lp.Model, lp.Usage, true
	}
	if lp.Message != nil && lp.Message.Usage != nil {
		return lp.Message.Model, lp.Message.Usage, true
	}
	return "", nil, false
}

// ParseTranscriptUsage sums true token usage across every assistant turn in a single Claude Code
// session transcript reader (JSONL, one object per line), including all subagent turns present in
// that reader. It returns the flat input + cache_creation + cache_read + output totals and the
// number of turns counted. It is the model-agnostic view of ParseByModel: the flat totals are the
// per-model buckets summed across every model. Lines without a usage object are skipped; malformed
// lines are tolerated (best-effort, see usageRaw). For per-model true-cost accounting across the
// whole session (main transcript + subagent transcripts) use ParseByModel / Account.
func ParseTranscriptUsage(r io.Reader) (Usage, error) {
	models, _, err := ParseByModel(r)
	if err != nil {
		return Usage{}, err
	}
	acct := Accounting{Models: models}
	return acct.Flatten(), nil
}
