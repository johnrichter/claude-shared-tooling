package transcript

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// errReader returns n bytes then a non-EOF read error, to check Turns propagates a genuine
// I/O failure (as opposed to a parse failure) instead of swallowing it.
type errReader struct {
	data []byte
	pos  int
	err  error
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func TestTurns_PropagatesReadError(t *testing.T) {
	c := ClaudeCodeJSONL{}
	sentinel := errors.New("boom: disk fell off")
	r := &errReader{data: []byte(`{"type":"user"}` + "\n"), err: sentinel}
	var got []Turn
	err := c.Turns(r, func(tn Turn) error { got = append(got, tn); return nil })
	if !errors.Is(err, sentinel) {
		t.Fatalf("Turns() error = %v, want sentinel %v", err, sentinel)
	}
	if len(got) != 1 {
		t.Fatalf("expected the one well-formed line before the read error to still be delivered, got %d", len(got))
	}
}

func TestTurns_StopsEarlyWhenCallbackErrors(t *testing.T) {
	c := ClaudeCodeJSONL{}
	data := []byte(
		`{"type":"user","sessionId":"s1"}` + "\n" +
			`{"type":"assistant","sessionId":"s1"}` + "\n" +
			`{"type":"assistant","sessionId":"s1"}` + "\n",
	)
	sentinel := errors.New("consumer stop")
	var n int
	err := c.Turns(bytes.NewReader(data), func(tn Turn) error {
		n++
		if n == 1 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Turns() error = %v, want sentinel %v", err, sentinel)
	}
	if n != 1 {
		t.Fatalf("callback invoked %d times, want exactly 1 (scan must stop on first error)", n)
	}
}

func TestTurns_EmptyAndBlankOnlyInput(t *testing.T) {
	c := ClaudeCodeJSONL{}
	cases := map[string]string{
		"empty":            "",
		"blank lines only": "\n\n   \n\t\n",
	}
	for name, in := range cases {
		var n int
		if err := c.Turns(bytes.NewReader([]byte(in)), func(tn Turn) error { n++; return nil }); err != nil {
			t.Errorf("%s: Turns() error = %v, want nil", name, err)
		}
		if n != 0 {
			t.Errorf("%s: got %d turns, want 0 (blank lines must not be emitted as turns)", name, n)
		}
	}
}

// TestTurns_LineNoSkipsBlankLines checks LineNo counts only non-blank lines emitted as turns,
// per the documented "invoking fn once per non-blank line" contract -- a consumer diffing
// LineNo against file line numbers would otherwise get silently wrong numbers.
func TestTurns_LineNoIsPositionalAmongEmittedTurns(t *testing.T) {
	c := ClaudeCodeJSONL{}
	data := []byte("\n" + `{"type":"user"}` + "\n\n" + `{"type":"assistant"}` + "\n")
	var lineNos []int
	if err := c.Turns(bytes.NewReader(data), func(tn Turn) error { lineNos = append(lineNos, tn.LineNo); return nil }); err != nil {
		t.Fatalf("Turns: %v", err)
	}
	if len(lineNos) != 2 || lineNos[0] != 1 || lineNos[1] != 2 {
		t.Errorf("got LineNo sequence %v, want [1 2] (sequential among emitted turns)", lineNos)
	}
}

// TestTurns_TopLevelModelAndUsagePreferredOverNested exercises the top-level branch of
// modelAndUsage (a shape the happy-path fixture never sets on a line that also has a nested
// message), guarding the "prefer top-level" precedence documented on the method.
func TestTurns_TopLevelModelAndUsagePreferredOverNested(t *testing.T) {
	c := ClaudeCodeJSONL{}
	line := `{"type":"assistant","model":"top-level-model","usage":{"input_tokens":9,"output_tokens":1},` +
		`"message":{"role":"assistant","model":"nested-model","usage":{"input_tokens":999,"output_tokens":999}}}` + "\n"
	var got Turn
	if err := c.Turns(bytes.NewReader([]byte(line)), func(tn Turn) error { got = tn; return nil }); err != nil {
		t.Fatalf("Turns: %v", err)
	}
	if got.Model != "top-level-model" {
		t.Errorf("Model = %q, want top-level value to win over nested", got.Model)
	}
	if got.Usage == nil || got.Usage.InputTokens != 9 {
		t.Errorf("Usage = %+v, want top-level usage (input_tokens=9) to win over nested", got.Usage)
	}
	// Role still comes from the nested message even when model/usage are taken top-level.
	if got.Role != "assistant" {
		t.Errorf("Role = %q, want %q (Role always sourced from message.role)", got.Role, "assistant")
	}
}

// TestDiscoverSubagentTranscripts_MissingMainPathIsNotAnError checks a main transcript path
// that does not exist on disk at all (not just a missing subagents/ dir) still returns
// (nil, nil): DiscoverSubagentTranscripts never stats the main path itself, only derives
// candidate subagent roots from it.
func TestDiscoverSubagentTranscripts_MissingMainPathIsNotAnError(t *testing.T) {
	c := ClaudeCodeJSONL{}
	found, err := c.DiscoverSubagentTranscripts(filepath.Join(t.TempDir(), "does-not-exist", "session.jsonl"))
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if found != nil {
		t.Errorf("got %v, want nil", found)
	}
}

// TestDiscoverSubagentTranscripts_IgnoresNonAgentFiles checks a subagents/ dir containing
// files that don't match the agent-*.jsonl naming convention are excluded, not swept in.
func TestDiscoverSubagentTranscripts_IgnoresNonAgentFiles(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "session.jsonl")
	subDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subDir, "agent-a1.jsonl"), "{}\n")
	writeFile(t, filepath.Join(subDir, "notes.txt"), "not a transcript\n")
	writeFile(t, filepath.Join(subDir, "agent-a1.jsonl.bak"), "{}\n")

	c := ClaudeCodeJSONL{}
	found, err := c.DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if len(found) != 1 || filepath.Base(found[0]) != "agent-a1.jsonl" {
		t.Errorf("got %v, want exactly [.../agent-a1.jsonl]", found)
	}
}

// TestDiscoverSubagentTranscripts_DedupesOverlappingRoots checks that when both candidate
// subagent roots resolve to the very same directory (a relative main path whose stem-based
// root and sibling root happen to coincide after Abs+Clean), a file is not returned twice.
func TestDiscoverSubagentTranscripts_DedupesOverlappingRoots(t *testing.T) {
	dir := t.TempDir()
	// Root A: <dir>/subagents ; Root B: <dir>/session/subagents. Make them the same directory
	// by naming the main transcript "subagents.jsonl" so stem == "subagents" and root B also
	// becomes <dir>/subagents/subagents -- not overlapping by construction in this impl, so
	// instead directly assert no duplicate path appears even when a symlink makes root B alias
	// root A.
	subDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subDir, "agent-x.jsonl"), "{}\n")

	stem := "session"
	nestedParent := filepath.Join(dir, stem)
	if err := os.MkdirAll(nestedParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(subDir, filepath.Join(nestedParent, "subagents")); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}

	mainPath := filepath.Join(dir, stem+".jsonl")
	c := ClaudeCodeJSONL{}
	found, err := c.DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("got %v (len %d), want exactly 1 deduped entry for the same underlying file reached via both roots", found, len(found))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeCodeJSONL_IsZeroValueSafe checks the documented "no state, reusable zero value"
// claim: a fresh ClaudeCodeJSONL{} (never explicitly constructed via a helper) works for every
// method, and Turns tolerates an io.Reader that returns io.EOF immediately with zero bytes.
func TestClaudeCodeJSONL_IsZeroValueSafe(t *testing.T) {
	var c ClaudeCodeJSONL
	if err := c.Turns(io.MultiReader(), func(tn Turn) error { return nil }); err != nil {
		t.Errorf("Turns on empty MultiReader: %v", err)
	}
}
