package toolchain

import (
	"encoding/json"
	"testing"
)

// TestQAVerifyParseUnparseableNonzeroExitNeverEmptyDiagnostics is an
// adversarial check for the test-strategy's negative case: garbage stdout
// (neither the gofmt bare-path shape nor the file:line:col: build shape) on
// a non-zero exit must never resolve to zero diagnostics — an empty
// diagnostic set on a failing exit is what would read as a silent pass one
// level up (classifyStatus only fails on counts.Errors/Warnings, never on
// exitCode alone once diags exist).
func TestQAVerifyParseUnparseableNonzeroExitNeverEmptyDiagnostics(t *testing.T) {
	a := NewGoAdapter()
	diags, err := a.Parse(2, []byte("some garbled tool crash output\nnot a path, not file:line:col"), []byte("panic: whatever"))
	if err != nil {
		t.Fatalf("Parse: unexpected error %v", err)
	}
	if len(diags) == 0 {
		t.Fatalf("Parse(exit=2, garbage) = %+v, want the fallback diagnostic, never empty", diags)
	}
	if diags[0].Severity != SeverityError {
		t.Errorf("fallback diagnostic severity = %q, want error", diags[0].Severity)
	}
}

// TestQAVerifyParseUnparseableZeroExitIsGenuinelyClean is the converse: an
// exit-0 run with garbage output (never gofmt/build shaped) legitimately has
// nothing to report — Parse must not manufacture a finding just because it
// could not classify a line, only because the tool signaled failure.
func TestQAVerifyParseUnparseableZeroExitIsGenuinelyClean(t *testing.T) {
	a := NewGoAdapter()
	diags, err := a.Parse(0, []byte("some informational banner\nnothing actionable here"), nil)
	if err != nil {
		t.Fatalf("Parse: unexpected error %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("Parse(exit=0, unrecognized banner) = %+v, want none (exit 0 with nothing recognized is a clean run)", diags)
	}
}

// TestQAVerifyDiagnosticWithNoLineOmitsLineFromJSON pins AC2's "records that
// fact rather than a placeholder": a Diagnostic naming a file but no line
// (govulncheck, gofmt -l) must serialize with the "line" key entirely
// absent, not present as 0 — 0 would read as a real (if odd) line number to
// any downstream JSON consumer, not as "position not reported".
func TestQAVerifyDiagnosticWithNoLineOmitsLineFromJSON(t *testing.T) {
	d := Diagnostic{Severity: SeverityError, Message: "known vulnerability reachable", File: "example.com/dep"}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, present := m["line"]; present {
		t.Errorf("Diagnostic JSON = %s, want no \"line\" key when the tool reports no position", raw)
	}
	if _, present := m["file"]; !present {
		t.Errorf("Diagnostic JSON = %s, want a \"file\" key naming the file", raw)
	}
}

// TestQAVerifyGovulncheckNeverReportsAPlaceholderLine pins AC2 for the one
// pair whose tool structurally never reports a position: govulncheck's own
// NDJSON finding carries no line at all, so parseGovulncheckStream must
// leave every diagnostic's Line at its unset zero value (which
// Diagnostic.json's omitempty then drops), never inventing one.
func TestQAVerifyGovulncheckNeverReportsAPlaceholderLine(t *testing.T) {
	stream := `{"finding":{"osv":"GO-2024-9999","trace":[{"package":"example.com/dep"}]}}`
	diags := parseGovulncheckStream([]byte(stream))
	if len(diags) != 1 {
		t.Fatalf("parseGovulncheckStream = %+v, want 1 diagnostic", diags)
	}
	if diags[0].File == "" {
		t.Errorf("diagnostic %+v names no file, want example.com/dep", diags[0])
	}
	if diags[0].Line != 0 {
		t.Errorf("diagnostic %+v carries line %d, want 0 — govulncheck reports no position", diags[0], diags[0].Line)
	}
}
