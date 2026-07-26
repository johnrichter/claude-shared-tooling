package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// ClaudeCodeJSONL is the TranscriptSource for Claude Code's session-log format: one JSON
// object per line (JSONL), turns nested under a `message` object, subagent turns marked by an
// `isSidechain` boolean, and subagent transcripts living in a `subagents/` directory keyed off
// the main transcript's path. It carries no state — every method is a pure function of its
// arguments — so a single zero-value ClaudeCodeJSONL{} is reused across every session.
type ClaudeCodeJSONL struct{}

var _ TranscriptSource = ClaudeCodeJSONL{}

// ResolvePath builds the transcript path Claude Code writes a session's log to: a plain join
// of the projects root, the scope key (Claude Code's slugified working-directory segment), and
// the session id, with the `.jsonl` extension the format uses.
func (ClaudeCodeJSONL) ResolvePath(root, scope, sessionID string) string {
	return filepath.Join(root, scope, sessionID+".jsonl")
}

// ccUsage mirrors the `usage` object Claude Code writes on an assistant turn. The nested
// CacheCreation split (5m/1h) is present on newer transcripts; when absent, the flat
// CacheCreationTokens is the turn's only cache-write signal and the 5m/1h fields stay zero.
type ccUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreation       *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u ccUsage) toUsage() *Usage {
	out := &Usage{
		InputTokens:         u.InputTokens,
		CacheCreationTokens: u.CacheCreationTokens,
		CacheReadTokens:     u.CacheReadTokens,
		OutputTokens:        u.OutputTokens,
	}
	if u.CacheCreation != nil {
		out.CacheCreationEphemeral5m = u.CacheCreation.Ephemeral5m
		out.CacheCreationEphemeral1h = u.CacheCreation.Ephemeral1h
	}
	return out
}

// ccLine is one Claude Code transcript line. IsSidechain is a pointer so a line that omits the
// field entirely (nil) is distinguishable from one that explicitly writes `false` — the
// distinction Authorship depends on. Model/Usage can sit at the top level or nested under
// `message`; the nested shape is what real assistant turns use.
type ccLine struct {
	Type        string    `json:"type"`
	SessionID   string    `json:"sessionId"`
	IsSidechain *bool     `json:"isSidechain"`
	Model       string    `json:"model"`
	Usage       *ccUsage  `json:"usage"`
	Message     *ccNested `json:"message"`
}

type ccNested struct {
	Role  string   `json:"role"`
	Model string   `json:"model"`
	Usage *ccUsage `json:"usage"`
}

// authorship resolves a line's Authorship from IsSidechain: absent (nil) stays AuthorUnknown,
// explicit false is AuthorOrchestrator, explicit true is AuthorSubagent. Nil never resolves to
// AuthorOrchestrator — see the TranscriptSource.Turns / Authorship doc for why that distinction
// must survive a future format change.
func (l ccLine) authorship() Authorship {
	if l.IsSidechain == nil {
		return AuthorUnknown
	}
	if *l.IsSidechain {
		return AuthorSubagent
	}
	return AuthorOrchestrator
}

// modelAndUsage picks the turn's model and usage, preferring the top-level fields when the
// line sets them and falling back to the message-nested shape otherwise.
func (l ccLine) modelAndUsage() (model string, usage *ccUsage) {
	if l.Model != "" || l.Usage != nil {
		return l.Model, l.Usage
	}
	if l.Message != nil {
		return l.Message.Model, l.Message.Usage
	}
	return "", nil
}

// Turns stream-parses r as Claude Code JSONL, invoking fn once per non-blank line in file
// order. A line that fails to unmarshal produces Turn{LineNo, Malformed: true, Flag: <reason>}
// — the scan continues past it rather than stopping or dropping it silently. A final line with
// no trailing newline (a transcript still being written) is parsed exactly like any other line.
func (c ClaudeCodeJSONL) Turns(r io.Reader, fn func(Turn) error) error {
	br := bufio.NewReaderSize(r, 1<<20)
	lineNo := 0
	for {
		raw, readErr := br.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 {
			lineNo++
			if err := fn(c.parseLine(trimmed, lineNo)); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// parseLine turns one non-blank JSONL line into a Turn, flagging rather than failing on a line
// that does not unmarshal as JSON at all (the only case a Claude Code line can be structurally
// broken — every field below it is optional).
func (c ClaudeCodeJSONL) parseLine(line []byte, lineNo int) Turn {
	var l ccLine
	if err := json.Unmarshal(line, &l); err != nil {
		return Turn{LineNo: lineNo, Malformed: true, Flag: fmt.Sprintf("malformed JSONL: %v", err)}
	}
	model, usage := l.modelAndUsage()
	t := Turn{
		LineNo:     lineNo,
		Type:       l.Type,
		Model:      model,
		SessionID:  l.SessionID,
		Authorship: l.authorship(),
	}
	if l.Message != nil {
		t.Role = l.Message.Role
	}
	if usage != nil {
		t.Usage = usage.toUsage()
	}
	return t
}

// subagentRoots returns the candidate subagents/ directories under a main transcript's path:
// the sibling layout (<dir>/subagents) and the live Claude Code layout, where subagents sit
// under a directory named after the session id (<dir>/<session-id>/subagents) — the main
// transcript's basename without its extension.
func subagentRoots(mainPath string) []string {
	dir := filepath.Dir(mainPath)
	stem := trimExt(filepath.Base(mainPath))
	return []string{
		filepath.Join(dir, "subagents"),
		filepath.Join(dir, stem, "subagents"),
	}
}

func trimExt(name string) string {
	ext := filepath.Ext(name)
	return name[:len(name)-len(ext)]
}

// DiscoverSubagentTranscripts recursively walks every candidate subagents/ root under path and
// returns every agent-*.jsonl file found at any depth — direct subagents and any nested-workflow
// subagent a spawn pattern introduces, with no fixed-depth assumption. A root that does not
// exist on disk contributes nothing. Results are deduped by cleaned absolute path and sorted.
func (c ClaudeCodeJSONL) DiscoverSubagentTranscripts(path string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, root := range subagentRoots(path) {
		if err := walkAgentFiles(root, seen, &out); err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func walkAgentFiles(root string, seen map[string]bool, out *[]string) error {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if matched, _ := filepath.Match("agent-*.jsonl", d.Name()); !matched {
			return nil
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		abs = filepath.Clean(abs)
		if !seen[abs] {
			seen[abs] = true
			*out = append(*out, abs)
		}
		return nil
	})
}
