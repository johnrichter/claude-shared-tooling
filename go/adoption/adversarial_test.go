package adoption

import (
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// TestClassifyFirstOperationWinsInRegistryOrder checks the documented precedence: an invocation
// is tested against registry in order, CLIMatch before RawMatch per operation, and the first
// operation to match either wins even if a later operation would also match.
func TestClassifyFirstOperationWinsInRegistryOrder(t *testing.T) {
	inv := Invocation{ToolName: "Bash", Input: map[string]any{"command": "shared-tool run"}}
	registry := []GovernedOperation{
		{Name: "first", CLIMatch: func(Invocation) bool { return true }, RawMatch: func(Invocation) bool { return false }},
		{Name: "second", CLIMatch: func(Invocation) bool { return true }, RawMatch: func(Invocation) bool { return false }},
	}
	out := Classify(registry, []Invocation{inv})
	if len(out) != 1 || out[0].Operation != "first" {
		t.Fatalf("Classify = %+v, want single classification against %q", out, "first")
	}
}

// TestClassifyUnmatchedInvocationProducesNoClassification checks an invocation matching neither
// matcher of any registry entry carries no adoption signal, per Classification's doc.
func TestClassifyUnmatchedInvocationProducesNoClassification(t *testing.T) {
	inv := Invocation{ToolName: "Read", Input: map[string]any{"file_path": "/tmp/x"}}
	registry := fixtureRegistry()
	out := Classify(registry, []Invocation{inv})
	if len(out) != 0 {
		t.Errorf("Classify(unmatched) = %+v, want empty", out)
	}
}

// TestRateRejectsOutOfRangeGate checks Rate's own input validation at both boundaries.
func TestRateRejectsOutOfRangeGate(t *testing.T) {
	for _, bad := range []int{-1, 101} {
		if _, err := Rate(nil, bad); err == nil {
			t.Errorf("Rate(gatePercent=%d) = nil error, want range error", bad)
		}
	}
	for _, ok := range []int{0, 100} {
		if _, err := Rate(nil, ok); err != nil {
			t.Errorf("Rate(gatePercent=%d) = %v, want no error", ok, err)
		}
	}
}

// TestRateOmitsOperationsWithNoClassifiedInvocations checks an operation absent from
// classifications never appears in Rate's result map (0% would be indistinguishable from
// measured non-adoption, per Rate's doc).
func TestRateOmitsOperationsWithNoClassifiedInvocations(t *testing.T) {
	adoption, err := Rate(nil, PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if len(adoption) != 0 {
		t.Errorf("Rate(no classifications) = %+v, want empty map", adoption)
	}
}

// TestRateBoundaryOneInvocationBelowGateFails checks a rate one invocation short of an 80% gate
// (3/4 = 75%) fails, distinguishing a near-miss from the exact-gate pass already covered by the
// fixture-based sanity test's 4/5 = 80% case.
func TestRateBoundaryOneInvocationBelowGateFails(t *testing.T) {
	classifications := []Classification{
		{Operation: "op", Route: RouteCLI}, {Operation: "op", Route: RouteCLI}, {Operation: "op", Route: RouteCLI},
		{Operation: "op", Route: RouteRaw},
	}
	adoption, err := Rate(classifications, PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	op := adoption["op"]
	if op.MetGate() {
		t.Errorf("op at 75%% must not meet an %d%% gate, got verdict=%s", PhaseAStartGatePercent, op.Verdict)
	}
	if op.Total() != 4 {
		t.Errorf("Total() = %d, want 4", op.Total())
	}
}

// TestCheckFloorEmptyRecordsNoViolations checks the zero-record case is not mistaken for a
// violation by omission bugs (e.g. an accidental len()==0-means-violation flip).
func TestCheckFloorEmptyRecordsNoViolations(t *testing.T) {
	if v := CheckFloor(nil); len(v) != 0 {
		t.Errorf("CheckFloor(nil) = %v, want no violations", v)
	}
}

// TestCheckFloorCatchesEveryViolationNotJustFirst checks CheckFloor is not short-circuiting: two
// tool-existence-denial records in the same slice must both surface, so a caller cannot undercount
// the floor break via an early-return bug.
func TestCheckFloorCatchesEveryViolationNotJustFirst(t *testing.T) {
	records := []HookEvalRecord{
		{SessionID: "s1", ToolName: "Bash", DeniesToolExists: true},
		{SessionID: "s2", ToolName: "Read", DeniesToolExists: false},
		{SessionID: "s3", ToolName: "Write", DeniesToolExists: true},
	}
	v := CheckFloor(records)
	if len(v) != 2 {
		t.Fatalf("CheckFloor = %d violations, want 2", len(v))
	}
	if v[0].Record.SessionID != "s1" || v[1].Record.SessionID != "s3" {
		t.Errorf("violations = %+v, want s1 then s3 in record order", v)
	}
}

// TestReadHookEvalRecordsMalformedLineReportsLineNumber checks a malformed hook-eval log line is
// a reported error (unlike a malformed transcript line, which ExtractInvocations tolerates) since
// CheckFloor's guarantee depends on every record in the log being read.
func TestReadHookEvalRecordsMalformedLineReportsLineNumber(t *testing.T) {
	r := strings.NewReader("{\"session_id\":\"s1\",\"tool_name\":\"Bash\"}\nnot json\n")
	_, err := ReadHookEvalRecords(r)
	if err == nil {
		t.Fatal("ReadHookEvalRecords(malformed) = nil error, want error naming line 2")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error = %v, want it to name line 2", err)
	}
}

// TestReportHookFiringUnknownOutcomeCountsNotApplicable checks an unrecognized Outcome value
// falls into the safe NotApplicable bucket rather than being silently dropped or miscounted as
// Fired.
func TestReportHookFiringUnknownOutcomeCountsNotApplicable(t *testing.T) {
	rep := ReportHookFiring([]HookEvalRecord{{Outcome: HookOutcome("something_else")}})
	if rep.NotApplicable != 1 || rep.Fired != 0 || rep.FailedOpen != 0 {
		t.Errorf("ReportHookFiring(unknown outcome) = %+v, want 1 not_applicable", rep)
	}
	if _, ok := rep.FiringRate(); ok {
		t.Error("FiringRate() ok=true with zero fired+failed_open records, want false")
	}
}

// TestFloorViolationGovernsOverBelowGateOperation checks Report.Result's documented precedence:
// a hard-floor violation reports precondition_unmet even when an operation is also below its
// gate in the same run - the floor is never traded off against, or merged with, a gate failure.
func TestFloorViolationGovernsOverBelowGateOperation(t *testing.T) {
	adoption, err := Rate([]Classification{{Operation: "gh", Route: RouteRaw}}, PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}
	if adoption["gh"].MetGate() {
		t.Fatal("test setup: gh must be below gate")
	}
	rep := Report{
		GatePercent:     PhaseAStartGatePercent,
		Adoption:        adoption,
		FloorViolations: []HardFloorViolation{{Record: HookEvalRecord{SessionID: "s1", ToolName: "Bash"}}},
	}
	result, err := rep.Result([]string{"adoption-report"})
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if result.Status != "precondition_unmet" {
		t.Errorf("Status = %q, want precondition_unmet even with a below-gate operation present", result.Status)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %+v, want exactly 1 (the floor violation, not a merged gate error)", result.Errors)
	}
	if result.Errors[0].Code != "precondition_unmet.adoption.tool_existence_denied" {
		t.Errorf("Errors[0].Code = %q, want the floor-violation code", result.Errors[0].Code)
	}
}

// TestReportResultCleanRunWithFailedOpenReportsCaveat checks the third governing tier: no floor
// violation, no operation below gate, but a failed-open hook eval still surfaces as a caveat
// rather than a silent success.
func TestReportResultCleanRunWithFailedOpenReportsCaveat(t *testing.T) {
	rep := Report{
		GatePercent: PhaseAStartGatePercent,
		HookFiring:  HookFiringReport{Fired: 1, FailedOpen: 1},
	}
	result, err := rep.Result([]string{"adoption-report"})
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if result.Status != "caveats" {
		t.Errorf("Status = %q, want caveats", result.Status)
	}
	if len(result.Caveats) != 1 || result.Caveats[0].Code != "caveats.adoption.hook_failed_open" {
		t.Errorf("Caveats = %+v, want one hook_failed_open caveat", result.Caveats)
	}
}

// TestReportResultFullyCleanRunSucceeds checks the empty-report baseline: no classifications, no
// floor violations, no failed-open hook evals reports plain success.
func TestReportResultFullyCleanRunSucceeds(t *testing.T) {
	rep := Report{GatePercent: PhaseAStartGatePercent}
	result, err := rep.Result([]string{"adoption-report"})
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("Status = %q, want success", result.Status)
	}
}

// TestLoadSessionInvocationsMissingMainTranscriptErrors checks a session with no resolvable main
// transcript is a reported error, not a silently empty invocation list - a missing fixture file
// must never be mistaken for a session with zero tool calls.
func TestLoadSessionInvocationsMissingMainTranscriptErrors(t *testing.T) {
	source := transcript.ClaudeCodeJSONL{}
	_, err := LoadSessionInvocations(source, "testdata/transcripts", "proj", "does-not-exist")
	if err == nil {
		t.Fatal("LoadSessionInvocations(missing session) = nil error, want error")
	}
}

// TestRouteStringNamesBothRoutes checks String's two branches, not just whichever route a fixture
// happens to exercise elsewhere.
func TestRouteStringNamesBothRoutes(t *testing.T) {
	if got := RouteCLI.String(); got != "cli" {
		t.Errorf("RouteCLI.String() = %q, want cli", got)
	}
	if got := RouteRaw.String(); got != "raw" {
		t.Errorf("RouteRaw.String() = %q, want raw", got)
	}
}

// TestBuildReportPropagatesRateError checks BuildReport surfaces Rate's own gate-range validation
// error rather than swallowing it or panicking.
func TestBuildReportPropagatesRateError(t *testing.T) {
	if _, err := BuildReport(nil, nil, 101); err == nil {
		t.Fatal("BuildReport(gatePercent=101) = nil error, want the out-of-range error Rate returns")
	}
}

// TestBuildReportPlumbsGatePercentAndFloorTogether is an end-to-end composition check distinct
// from the sanity tests' fixture-driven pass: BuildReport must wire Rate's gate and CheckFloor
// together in one call rather than requiring a caller to invoke both separately and merge results.
func TestBuildReportPlumbsGatePercentAndFloorTogether(t *testing.T) {
	classifications := []Classification{{Operation: "op", Route: RouteCLI}}
	hookRecords := []HookEvalRecord{{SessionID: "s1", ToolName: "Bash", DeniesToolExists: true}}
	rep, err := BuildReport(classifications, hookRecords, PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("BuildReport: %v", err)
	}
	if rep.GatePercent != PhaseAStartGatePercent {
		t.Errorf("GatePercent = %d, want %d", rep.GatePercent, PhaseAStartGatePercent)
	}
	if len(rep.FloorViolations) != 1 {
		t.Errorf("FloorViolations = %+v, want 1", rep.FloorViolations)
	}
	if rep.Adoption["op"].CLICount != 1 {
		t.Errorf("Adoption[op].CLICount = %d, want 1", rep.Adoption["op"].CLICount)
	}
}
