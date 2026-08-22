package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

func init() {
	Register(cargoAdapter{})
}

// cargoAdapter is the Adapter for Rust crates, run through cargo. It is the
// proven top context-injector among language tools — an unfiltered cargo
// run can emit thousands of lines — so its Parse is the one every other
// adapter's cap-and-log behavior (run.go) exists to bound.
type cargoAdapter struct{}

func (cargoAdapter) Language() string        { return "rust" }
func (cargoAdapter) Route(check Check) Route { return RouteSubprocess }
func (cargoAdapter) Tool(check Check) string { return "cargo" }

// RunInProcess is unreachable through Run — cargo spawns every check it
// supports — and reports the unsupported-check error to a direct caller.
func (cargoAdapter) RunInProcess(_ context.Context, target Target) ([]Diagnostic, error) {
	return nil, errUnsupportedCheck("cargo", target.Check)
}

// Command returns cargo's argv for check. build and lint both request
// --message-format=json, giving Parse the compiler's own structured
// diagnostics rather than its human-rendered text. test requests the same
// format for its compile phase; the test harness itself still prints plain
// text on the stable toolchain, so Parse also recognizes that plain-text
// failure line.
func (cargoAdapter) Command(check Check) ([]string, error) {
	switch check {
	case CheckBuild:
		return []string{"build", "--message-format=json"}, nil
	case CheckTest:
		return []string{"test", "--message-format=json"}, nil
	case CheckLint:
		return []string{"clippy", "--message-format=json", "--all-targets", "--all-features"}, nil
	default:
		return nil, errUnsupportedCheck("cargo", check)
	}
}

// cargoMessage is the subset of cargo's --message-format=json event shape
// (https://doc.rust-lang.org/cargo/reference/external-tools.html) Parse
// needs: a compiler diagnostic's level, text, optional lint code, and the
// file/line of its primary span.
type cargoMessage struct {
	Reason  string `json:"reason"`
	Message struct {
		Message string `json:"message"`
		Level   string `json:"level"`
		Code    *struct {
			Code string `json:"code"`
		} `json:"code"`
		Spans []struct {
			FileName  string `json:"file_name"`
			LineStart int    `json:"line_start"`
			IsPrimary bool   `json:"is_primary"`
		} `json:"spans"`
	} `json:"message"`
}

// cargoTestFailureRE matches libtest's plain-text per-test failure line
// (`test <path::to::test> ... FAILED`), the one result cargo test does not
// carry in its JSON stream on the stable toolchain.
var cargoTestFailureRE = regexp.MustCompile(`^test (\S+) \.\.\. FAILED$`)

// Parse reads cargo's stdout line by line: each line is either a
// --message-format=json event or, during a test run, one of the test
// harness's own plain-text lines. A line that is neither yields nothing —
// cargo's build progress and summary lines are expected noise, not an
// unparseable-output error.
func (cargoAdapter) Parse(exitCode int, stdout, stderr []byte) ([]Diagnostic, error) {
	var diags []Diagnostic
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg cargoMessage
		if err := json.Unmarshal(line, &msg); err == nil && msg.Reason == "compiler-message" {
			if d, ok := diagnosticFromCargoMessage(msg); ok {
				diags = append(diags, d)
			}
			continue
		}
		if m := cargoTestFailureRE.FindStringSubmatch(string(line)); m != nil {
			diags = append(diags, Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("test failed: %s", m[1])})
		}
	}
	if len(diags) == 0 && exitCode != 0 {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("cargo exited %d with no parsed diagnostics; see log_ref for raw output", exitCode),
		})
	}
	return diags, nil
}

// diagnosticFromCargoMessage converts one compiler-message event into a
// Diagnostic, or reports false for a level Parse doesn't count (cargo also
// emits "note", "help" and "failure-note" as their own top-level events for
// a diagnostic's children — only its own error/warning level is counted
// once).
func diagnosticFromCargoMessage(msg cargoMessage) (Diagnostic, bool) {
	var severity Severity
	switch msg.Message.Level {
	case "error":
		severity = SeverityError
	case "warning":
		severity = SeverityWarning
	default:
		return Diagnostic{}, false
	}
	d := Diagnostic{Severity: severity, Message: msg.Message.Message}
	if msg.Message.Code != nil {
		d.Code = msg.Message.Code.Code
	}
	for _, span := range msg.Message.Spans {
		if span.IsPrimary {
			d.File = span.FileName
			d.Line = span.LineStart
			break
		}
	}
	return d, true
}
