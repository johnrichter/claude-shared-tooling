package adoption

import (
	"os"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// gitOp and worktreeOp exercise a governed-CLI-vs-raw-tool pair the way a real toolchain package
// would define it: distinct subcommand prefixes on the same "-tools" wrapper family, so a
// caller's own operation set is the only thing Classify ever hardcodes.
func fixtureRegistry() []GovernedOperation {
	hasCmd := func(inv Invocation, prefix string) bool {
		if inv.ToolName != "Bash" {
			return false
		}
		cmd, _ := inv.Input["command"].(string)
		return strings.HasPrefix(cmd, prefix)
	}
	return []GovernedOperation{
		{
			Name:     "worktree",
			CLIMatch: func(inv Invocation) bool { return hasCmd(inv, "git-tools worktree") },
			RawMatch: func(inv Invocation) bool { return hasCmd(inv, "git worktree") },
		},
		{
			Name: "git",
			CLIMatch: func(inv Invocation) bool {
				return hasCmd(inv, "git-tools ") && !hasCmd(inv, "git-tools worktree")
			},
			RawMatch: func(inv Invocation) bool {
				return (hasCmd(inv, "git ") || inv.Input["command"] == "git") && !hasCmd(inv, "git-tools")
			},
		},
		{
			Name:     "gh",
			CLIMatch: func(inv Invocation) bool { return hasCmd(inv, "gh-tools ") },
			RawMatch: func(inv Invocation) bool { return hasCmd(inv, "gh ") },
		},
	}
}

// TestSanityClassifyRoutesCLIVsRawOnFixtures checks Classify against the frozen fixture
// transcripts: session-a's main transcript plus its subagent fan-out give the "git" operation an
// exact 4/5 (80%) CLI adoption rate and "worktree" a clean 2/2; session-b's raw-heavy "gh" usage
// gives it 1/5 (20%).
func TestSanityClassifyRoutesCLIVsRawOnFixtures(t *testing.T) {
	source := transcript.ClaudeCodeJSONL{}
	root := "testdata/transcripts"

	invA, err := LoadSessionInvocations(source, root, "proj", "session-a")
	if err != nil {
		t.Fatalf("LoadSessionInvocations(session-a): %v", err)
	}
	invB, err := LoadSessionInvocations(source, root, "proj", "session-b")
	if err != nil {
		t.Fatalf("LoadSessionInvocations(session-b): %v", err)
	}

	registry := fixtureRegistry()
	classifications := Classify(registry, append(invA, invB...))

	adoption, err := Rate(classifications, PhaseAStartGatePercent)
	if err != nil {
		t.Fatalf("Rate: %v", err)
	}

	git, ok := adoption["git"]
	if !ok {
		t.Fatal(`adoption["git"] missing`)
	}
	if git.CLICount != 4 || git.RawCount != 1 {
		t.Errorf("git counts = %d cli, %d raw, want 4, 1", git.CLICount, git.RawCount)
	}
	if !git.MetGate() {
		t.Errorf("git at exactly the %d%% gate should meet it, verdict=%s", PhaseAStartGatePercent, git.Verdict)
	}

	worktree, ok := adoption["worktree"]
	if !ok {
		t.Fatal(`adoption["worktree"] missing`)
	}
	if worktree.CLICount != 2 || worktree.RawCount != 0 || !worktree.MetGate() {
		t.Errorf("worktree = %+v, want 2 cli, 0 raw, gate met", worktree)
	}

	gh, ok := adoption["gh"]
	if !ok {
		t.Fatal(`adoption["gh"] missing`)
	}
	if gh.CLICount != 1 || gh.RawCount != 4 {
		t.Errorf("gh counts = %d cli, %d raw, want 1, 4", gh.CLICount, gh.RawCount)
	}
	if gh.MetGate() {
		t.Errorf("gh at 20%% adoption must not meet an %d%% gate", PhaseAStartGatePercent)
	}

	result, err := Report{GatePercent: PhaseAStartGatePercent, Adoption: adoption}.Result([]string{"adoption-report"})
	if err != nil {
		t.Fatalf("Report.Result: %v", err)
	}
	if result.Status != "gate_negative" {
		t.Errorf("Result.Status = %q, want gate_negative (gh below gate)", result.Status)
	}
}

// TestSanityCheckFloorFailsOnToolExistenceDenial checks the hard floor against both hook-eval
// fixtures: the clean set (fired + failed_open + not_applicable, none denying existence) must
// produce no violation, and the floor_violation fixture's one denies_tool_exists record must be
// caught regardless of its outcome otherwise looking like a normal "fired" record.
func TestSanityCheckFloorFailsOnToolExistenceDenial(t *testing.T) {
	clean := readHookFixture(t, "testdata/hookeval/clean.jsonl")
	if v := CheckFloor(clean); len(v) != 0 {
		t.Errorf("CheckFloor(clean) = %v, want no violations", v)
	}
	firing := ReportHookFiring(clean)
	if firing.Fired != 1 || firing.FailedOpen != 1 || firing.NotApplicable != 1 {
		t.Errorf("ReportHookFiring(clean) = %+v, want 1 fired, 1 failed_open, 1 not_applicable", firing)
	}

	broken := readHookFixture(t, "testdata/hookeval/floor_violation.jsonl")
	violations := CheckFloor(broken)
	if len(violations) != 1 {
		t.Fatalf("CheckFloor(floor_violation) = %d violations, want 1", len(violations))
	}
	if violations[0].Record.ToolName != "Bash" || violations[0].Record.SessionID != "session-b" {
		t.Errorf("violation record = %+v, want the session-b Bash record", violations[0].Record)
	}

	report := Report{GatePercent: PhaseAStartGatePercent, FloorViolations: violations}
	result, err := report.Result([]string{"adoption-report"})
	if err != nil {
		t.Fatalf("Report.Result: %v", err)
	}
	if result.Status != "precondition_unmet" {
		t.Errorf("Result.Status = %q, want precondition_unmet (floor violation governs over any gate outcome)", result.Status)
	}
}

func readHookFixture(t *testing.T, path string) []HookEvalRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	records, err := ReadHookEvalRecords(f)
	if err != nil {
		t.Fatalf("ReadHookEvalRecords(%s): %v", path, err)
	}
	return records
}
