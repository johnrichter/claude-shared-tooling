package adoption

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// Route is which path an Invocation took to reach a governed operation's effect: through the
// operation's sanctioned CLI, or through the raw tool call the CLI exists to replace.
type Route int

const (
	// RouteRaw means the invocation reached the operation's effect through the raw tool call
	// the governed CLI supersedes.
	RouteRaw Route = iota
	// RouteCLI means the invocation went through the operation's sanctioned CLI.
	RouteCLI
)

// String names a Route for logging and report rendering.
func (r Route) String() string {
	if r == RouteCLI {
		return "cli"
	}
	return "raw"
}

// Invocation is one tool call an agent transcript recorded, in the shape Classify needs: which
// tool Claude Code invoked, with what input, and where in the transcript it happened. Input is
// the tool's raw `input` object, whatever shape that particular tool defines - Classify's
// matchers are the only place that shape is interpreted.
type Invocation struct {
	SessionID  string
	Path       string
	LineNo     int
	Authorship transcript.Authorship
	ToolName   string
	Input      map[string]any
}

// GovernedOperation names one capability this codebase has moved behind a CLI and the two
// matchers Classify tests an Invocation against: CLIMatch for the sanctioned route, RawMatch for
// the raw route it superseded. Neither matcher is hardcoded by this package - a caller supplies
// the registry, the same way gate.Band never hardcodes a floor or ceiling.
type GovernedOperation struct {
	// Name identifies the operation in every Classification and adoption Report it appears in.
	Name string
	// CLIMatch reports whether inv is this operation's sanctioned CLI invocation.
	CLIMatch func(inv Invocation) bool
	// RawMatch reports whether inv is the raw tool call this operation's CLI supersedes.
	RawMatch func(inv Invocation) bool
}

// Classification is one Invocation's outcome against a Registry: which GovernedOperation it
// matched and the Route it took. An Invocation matching neither a CLIMatch nor a RawMatch of any
// operation in the registry carries no adoption signal and is never produced.
type Classification struct {
	Invocation Invocation
	Operation  string
	Route      Route
}

// Classify matches every invocation against registry, in registry order, and returns one
// Classification per match: CLIMatch is tried before RawMatch, and the first operation to match
// either wins - an invocation is never classified against more than one operation.
func Classify(registry []GovernedOperation, invocations []Invocation) []Classification {
	var out []Classification
	for _, inv := range invocations {
		for _, op := range registry {
			if op.CLIMatch(inv) {
				out = append(out, Classification{Invocation: inv, Operation: op.Name, Route: RouteCLI})
				break
			}
			if op.RawMatch(inv) {
				out = append(out, Classification{Invocation: inv, Operation: op.Name, Route: RouteRaw})
				break
			}
		}
	}
	return out
}

// ccToolUseLine is the slice of a Claude Code transcript line this package reads that
// transcript.Turn does not carry: the assistant message's tool_use content blocks. transcript
// stays format-generic across turn accounting; a tool call's name and input are adoption's own,
// narrower concern.
type ccToolUseLine struct {
	SessionID   string `json:"sessionId"`
	IsSidechain *bool  `json:"isSidechain"`
	Message     *struct {
		Content []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

func (l ccToolUseLine) authorship() transcript.Authorship {
	if l.IsSidechain == nil {
		return transcript.AuthorUnknown
	}
	if *l.IsSidechain {
		return transcript.AuthorSubagent
	}
	return transcript.AuthorOrchestrator
}

// ExtractInvocations reads r as Claude Code transcript JSONL and returns every tool_use block it
// finds, tagged with path for Invocation.Path. A blank line is skipped; a line that fails to
// parse as JSON is skipped as well - a fixture-classification pass over a frozen, already-valid
// transcript has no use for transcript's Malformed-line reporting, and skipping keeps a stray
// malformed line from stopping classification of everything after it.
func ExtractInvocations(r io.Reader, path string) ([]Invocation, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var out []Invocation
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var l ccToolUseLine
		if err := json.Unmarshal(line, &l); err != nil || l.Message == nil {
			continue
		}
		authorship := l.authorship()
		for _, block := range l.Message.Content {
			if block.Type != "tool_use" {
				continue
			}
			out = append(out, Invocation{
				SessionID:  l.SessionID,
				Path:       path,
				LineNo:     lineNo,
				Authorship: authorship,
				ToolName:   block.Name,
				Input:      block.Input,
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("adoption: read transcript %s: %w", path, err)
	}
	return out, nil
}

// LoadSessionInvocations is the harness a fixture-classification run calls once per session: it
// resolves the session's main transcript path through source, extracts that transcript's
// invocations, discovers every subagent transcript source found underneath it (at any nesting
// depth) and extracts theirs too, so a subagent's raw-tool fallback is measured exactly like the
// orchestrator's own. root and scope are source's own path-resolution inputs (see
// transcript.TranscriptSource.ResolvePath); a missing main transcript is reported as an error,
// a missing subagents directory contributes nothing, per DiscoverSubagentTranscripts.
func LoadSessionInvocations(source transcript.TranscriptSource, root, scope, sessionID string) ([]Invocation, error) {
	mainPath := source.ResolvePath(root, scope, sessionID)
	invocations, err := extractInvocationsFromFile(mainPath)
	if err != nil {
		return nil, err
	}

	subPaths, err := source.DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		return nil, fmt.Errorf("adoption: discover subagent transcripts for %s: %w", mainPath, err)
	}
	for _, subPath := range subPaths {
		subInvocations, err := extractInvocationsFromFile(subPath)
		if err != nil {
			return nil, err
		}
		invocations = append(invocations, subInvocations...)
	}
	return invocations, nil
}

func extractInvocationsFromFile(path string) ([]Invocation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("adoption: open transcript %s: %w", path, err)
	}
	defer f.Close()
	return ExtractInvocations(f, path)
}
