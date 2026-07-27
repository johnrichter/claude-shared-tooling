package adoption

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// HookOutcome classifies what a governed PreToolUse routing hook did when it evaluated one tool
// invocation against the CLI-before-raw-tool routing rule (see clikit's TriageRunTool).
type HookOutcome string

const (
	// HookFired means the hook recognized a raw invocation of a governed operation, denied it
	// and redirected the caller to that operation's CLI.
	HookFired HookOutcome = "fired"
	// HookFailedOpen means a raw invocation of a governed operation reached the tool without
	// the hook denying it - the hook did not evaluate it, errored, or its rule did not match.
	HookFailedOpen HookOutcome = "failed_open"
	// HookNotApplicable means the invocation the hook evaluated was not a governed operation at
	// all: there was nothing to fire on, and letting it through was correct.
	HookNotApplicable HookOutcome = "not_applicable"
)

// HookEvalRecord is one PreToolUse routing-hook evaluation, as the hook itself logs it.
// DeniesToolExists is the hard-floor signal CheckFloor asserts on: it is true only when the
// hook's denial told the caller the tool does not exist, as distinct from denying the raw
// invocation and redirecting to a sanctioned CLI (Outcome == HookFired).
type HookEvalRecord struct {
	SessionID        string      `json:"session_id"`
	ToolName         string      `json:"tool_name"`
	Operation        string      `json:"operation"`
	Outcome          HookOutcome `json:"outcome"`
	DeniesToolExists bool        `json:"denies_tool_exists"`
}

// ReadHookEvalRecords parses r as newline-delimited JSON hook-eval records, one per PreToolUse
// evaluation. A blank line is skipped; a line that fails to parse is a reported error naming its
// 1-based line number, since a hook-eval log - unlike a frozen transcript fixture - is exactly
// the artifact CheckFloor's guarantee depends on, and a silently dropped line would understate a
// floor violation rather than report one.
func ReadHookEvalRecords(r io.Reader) ([]HookEvalRecord, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var out []HookEvalRecord
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec HookEvalRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("adoption: hook-eval line %d: %w", lineNo, err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("adoption: read hook-eval records: %w", err)
	}
	return out, nil
}

// HookFiringReport tallies HookEvalRecords by Outcome: the observable proxy for whether the
// routing rule actually ran, independent of what the transcript-side Rate measures.
type HookFiringReport struct {
	Fired         int
	FailedOpen    int
	NotApplicable int
}

// ReportHookFiring tallies records by Outcome. A record whose Outcome is not one of the three
// known values counts as NotApplicable, matching the safe default for an unrecognized log entry.
func ReportHookFiring(records []HookEvalRecord) HookFiringReport {
	var rep HookFiringReport
	for _, rec := range records {
		switch rec.Outcome {
		case HookFired:
			rep.Fired++
		case HookFailedOpen:
			rep.FailedOpen++
		default:
			rep.NotApplicable++
		}
	}
	return rep
}

// FiringRate returns the fraction of governed-relevant records (Fired+FailedOpen) where the hook
// fired, and false when there is no governed-relevant record to divide by.
func (r HookFiringReport) FiringRate() (rate float64, ok bool) {
	total := r.Fired + r.FailedOpen
	if total == 0 {
		return 0, false
	}
	return float64(r.Fired) / float64(total), true
}
