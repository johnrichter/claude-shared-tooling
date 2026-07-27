package transcript

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openFixture(t *testing.T, dir, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", dir, name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// TestClaudeCodeJSONL_TurnsParsesUsageModelSessionAndAuthorship checks Turns extracts model,
// usage, session id and authorship from a mixed fixture, and flags (not drops) a broken line.
func TestClaudeCodeJSONL_TurnsParsesUsageModelSessionAndAuthorship(t *testing.T) {
	c := ClaudeCodeJSONL{}
	f := openFixture(t, "session", "main.jsonl")

	var turns []Turn
	if err := c.Turns(f, func(tn Turn) error {
		turns = append(turns, tn)
		return nil
	}); err != nil {
		t.Fatalf("Turns: %v", err)
	}
	if len(turns) != 5 {
		t.Fatalf("got %d turns, want 5", len(turns))
	}

	orchestratorUser := turns[0]
	if orchestratorUser.Authorship != AuthorOrchestrator || orchestratorUser.SessionID != "sess-0001" {
		t.Errorf("turn 1: got authorship=%q session=%q", orchestratorUser.Authorship, orchestratorUser.SessionID)
	}

	assistantTurn := turns[1]
	if assistantTurn.Model != "claude-sonnet-5" || assistantTurn.Usage == nil {
		t.Fatalf("turn 2: got model=%q usage=%v", assistantTurn.Model, assistantTurn.Usage)
	}
	if assistantTurn.Usage.InputTokens != 100 || assistantTurn.Usage.CacheCreationEphemeral5m != 40 {
		t.Errorf("turn 2: got usage=%+v", *assistantTurn.Usage)
	}

	noMarker := turns[2]
	if noMarker.Authorship != AuthorUnknown {
		t.Errorf("turn 3 (no isSidechain field): got authorship=%q, want AuthorUnknown", noMarker.Authorship)
	}

	malformed := turns[3]
	if !malformed.Malformed || malformed.Flag == "" {
		t.Errorf("turn 4 (not-json-at-all): got malformed=%v flag=%q, want malformed with a reason", malformed.Malformed, malformed.Flag)
	}

	subagentTurn := turns[4]
	if subagentTurn.Authorship != AuthorSubagent {
		t.Errorf("turn 5 (isSidechain:true): got authorship=%q, want AuthorSubagent", subagentTurn.Authorship)
	}
}

// TestClaudeCodeJSONL_TurnsHandlesTruncatedFinalLine checks a final line with no trailing
// newline parses normally when it is valid JSON, and is flagged (not dropped, not a crash)
// when it is cut off mid-object.
func TestClaudeCodeJSONL_TurnsHandlesTruncatedFinalLine(t *testing.T) {
	c := ClaudeCodeJSONL{}

	f := openFixture(t, "lonesome", "truncated_valid.jsonl")
	var got []Turn
	if err := c.Turns(f, func(tn Turn) error { got = append(got, tn); return nil }); err != nil {
		t.Fatalf("Turns (no trailing newline, valid json): %v", err)
	}
	if len(got) != 1 || got[0].Malformed {
		t.Errorf("valid final line with no trailing newline should parse cleanly, got %+v", got)
	}

	f2 := openFixture(t, "lonesome", "truncated_partial.jsonl")
	var got2 []Turn
	if err := c.Turns(f2, func(tn Turn) error { got2 = append(got2, tn); return nil }); err != nil {
		t.Fatalf("Turns (cut-off json, no trailing newline): %v", err)
	}
	if len(got2) != 1 || !got2[0].Malformed {
		t.Errorf("cut-off final line should be flagged, not crash or drop, got %+v", got2)
	}
}

// TestClaudeCodeJSONL_DiscoverSubagentTranscripts checks discovery finds subagent transcripts
// at both a direct and a nested-workflow depth under a main transcript's path.
func TestClaudeCodeJSONL_DiscoverSubagentTranscripts(t *testing.T) {
	c := ClaudeCodeJSONL{}
	mainPath := filepath.Join("testdata", "session", "main.jsonl")

	found, err := c.DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("got %d subagent transcripts, want 2: %v", len(found), found)
	}
	for _, p := range found {
		if !strings.HasSuffix(p, ".jsonl") || !strings.Contains(filepath.Base(p), "agent-") {
			t.Errorf("unexpected discovered path: %s", p)
		}
	}
}

// TestClaudeCodeJSONL_DiscoverSubagentTranscripts_NoSubagents checks a main transcript with no
// subagents/ sibling returns a nil slice, not an error.
func TestClaudeCodeJSONL_DiscoverSubagentTranscripts_NoSubagents(t *testing.T) {
	c := ClaudeCodeJSONL{}
	found, err := c.DiscoverSubagentTranscripts(filepath.Join("testdata", "lonesome", "truncated_valid.jsonl"))
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if found != nil {
		t.Errorf("got %v, want nil for a path with no subagents/ sibling", found)
	}
}

// TestClaudeCodeJSONL_ResolvePath checks the resolved path is a plain join of root, scope and
// session id.
func TestClaudeCodeJSONL_ResolvePath(t *testing.T) {
	c := ClaudeCodeJSONL{}
	got := c.ResolvePath("/projects", "cwd-slug", "sess-0001")
	want := filepath.Join("/projects", "cwd-slug", "sess-0001.jsonl")
	if got != want {
		t.Errorf("ResolvePath() = %q, want %q", got, want)
	}
}

// stubSource is a second TranscriptSource implementation carrying no relation to Claude Code's
// on-disk shape, proving the interface is genuinely swappable rather than accidentally coupled
// to ClaudeCodeJSONL's internals. It ignores its reader entirely — its turns are fixed.
type stubSource struct{ turns []Turn }

var _ TranscriptSource = stubSource{}

func (s stubSource) ResolvePath(root, scope, sessionID string) string {
	return root + "/" + scope + "/" + sessionID
}

func (s stubSource) Turns(_ io.Reader, fn func(Turn) error) error {
	for _, t := range s.turns {
		if err := fn(t); err != nil {
			return err
		}
	}
	return nil
}

func (s stubSource) DiscoverSubagentTranscripts(path string) ([]string, error) { return nil, nil }

// TestInterfaceSwap_StubSourceSatisfiesTranscriptSource checks a second, unrelated
// TranscriptSource implementation works through the same interface a consumer would use.
func TestInterfaceSwap_StubSourceSatisfiesTranscriptSource(t *testing.T) {
	var src TranscriptSource = stubSource{turns: []Turn{{LineNo: 1, Authorship: AuthorUnknown}}}
	var got []Turn
	if err := src.Turns(nil, func(t Turn) error { got = append(got, t); return nil }); err != nil {
		t.Fatalf("Turns: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d turns from stub, want 1", len(got))
	}
}
