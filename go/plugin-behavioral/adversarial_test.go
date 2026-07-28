package plugin_behavioral

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// Independent adversarial pass by test-engineer verification, on top of sanity_test.go's own
// suite. Still hermetic: no test in this file spawns a real `claude` process.

// TestLiveOptedIn_EnvFalsyValuesDoNotOptIn proves the ambient opt-in env var has a narrow, exact
// falsy set -- an operator setting PLUGIN_BEHAVIORAL_LIVE=0 or =false to mean "not live" must not
// be silently overridden into a live run by a looser truthiness rule.
func TestLiveOptedIn_EnvFalsyValuesDoNotOptIn(t *testing.T) {
	for _, v := range []string{"", "0", "false", "False", "FALSE"} {
		t.Setenv(LiveOptInEnv, v)
		if LiveOptedIn(false) {
			t.Errorf("LiveOptedIn(false) with %s=%q = true, want false", LiveOptInEnv, v)
		}
	}
	for _, v := range []string{"1", "true", "yes"} {
		t.Setenv(LiveOptInEnv, v)
		if !LiveOptedIn(false) {
			t.Errorf("LiveOptedIn(false) with %s=%q = false, want true", LiveOptInEnv, v)
		}
	}
}

// TestRun_EnvOptInSatisfiesInterlockWithoutLiveFlag proves the interlock honors the ambient env
// var as an alternative to Options.Live=true, end to end through Run -- not just LiveOptedIn in
// isolation.
func TestRun_EnvOptInSatisfiesInterlockWithoutLiveFlag(t *testing.T) {
	t.Setenv(LiveOptInEnv, "1")
	opts := baseOptions(t)
	opts.Live = false
	opts.Run = fakeProbeRunner("sess-1", 0.01, "done")
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v, want the ambient env opt-in to satisfy the interlock", err)
	}
	if len(report.Records) != 1 {
		t.Fatalf("Records = %+v, want exactly one", report.Records)
	}
}

// TestMissingCoverage_DedupesAndSortsDuplicateManifestIDs proves the accounting figure is safe
// against a sloppy manifest that names the same case id twice: it must not double-report.
func TestMissingCoverage_DedupesAndSortsDuplicateManifestIDs(t *testing.T) {
	got := MissingCoverage([]string{"c3", "c1", "c1", "c2", "c3"}, []string{"c2"})
	want := []string{"c1", "c3"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("MissingCoverage = %v, want %v", got, want)
	}
}

// TestMissingCoverage_EmptyManifestIsEmptyNotNilPanic guards the zero-manifest edge: a run with no
// declared manifest coverage must report no gap, not error or panic.
func TestMissingCoverage_EmptyManifestIsEmptyNotNilPanic(t *testing.T) {
	if got := MissingCoverage(nil, []string{"c1"}); len(got) != 0 {
		t.Fatalf("MissingCoverage(nil, ...) = %v, want empty", got)
	}
}

// TestAssertPluginActive_MalformedJSONFailsLoud proves a tracked-settings file that is not valid
// JSON is reported as its own distinct error, never silently treated as "plugin inactive" or
// (worse) skipped.
func TestAssertPluginActive_MalformedJSONFailsLoud(t *testing.T) {
	path := writeMalformedSettings(t)
	err := AssertPluginActive("my-plugin@mp", path)
	if err == nil {
		t.Fatal("AssertPluginActive returned nil for malformed JSON, want a loud error")
	}
	var inactive *PluginInactiveError
	if errors.As(err, &inactive) {
		t.Fatal("a malformed settings file must not be reported as the narrower PluginInactiveError -- it is a distinct read/parse failure")
	}
}

func writeMalformedSettings(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write malformed settings: %v", err)
	}
	return path
}

// TestCapture_IsErrorResultFailsLoud proves a probe whose own JSON result reports is_error=true is
// never graded as a successful capture -- it must propagate as an error so the caller's fail-safe
// path (Violated) takes over, rather than a classifier grading a failed probe as if it were a
// clean, empty reply.
func TestCapture_IsErrorResultFailsLoud(t *testing.T) {
	opts := baseOptions(t)
	opts.Run = func(context.Context, []string, sysops.Options) (*sysops.Result, error) {
		return &sysops.Result{ExitCode: 0, Stdout: []byte(`{"session_id":"s1","total_cost_usd":0.01,"is_error":true,"result":""}`)}, nil
	}
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Violated record (is_error must fail-safe, never a silent pass)", report.Records)
	}
}

// TestCapture_NonJSONStdoutFailsLoud proves a probe whose stdout is not the expected JSON shape
// (a crash, a malformed CLI upgrade) is graded Violated rather than the classifier running against
// a zero-value ProbeObservation that could accidentally read as compliant.
func TestCapture_NonJSONStdoutFailsLoud(t *testing.T) {
	opts := baseOptions(t)
	opts.Run = func(context.Context, []string, sysops.Options) (*sysops.Result, error) {
		return &sysops.Result{ExitCode: 0, Stdout: []byte("not json at all")}, nil
	}
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Violated record (non-JSON stdout must fail-safe)", report.Records)
	}
}

// TestRun_AgenticMechanismFailureNeverObservesTranscript proves the $0-before-spend ordering
// inside an agentic case: when the mechanism check itself fails, Observe must never be called --
// mirroring the probe-side leakage-lint-before-spend guarantee for the agentic path.
func TestRun_AgenticMechanismFailureNeverObservesTranscript(t *testing.T) {
	observeCalled := false
	opts := baseOptions(t)
	opts.Live = false
	opts.Cases = []Case{{
		ID:        "case-agentic",
		Kind:      KindAgentic,
		Mechanism: FileExists("/does/not/exist/at/all"),
		Observe: func(transcript.TranscriptSource, string, string, string) (ClassifierResult, error) {
			observeCalled = true
			return ClassifierResult{Outcome: Honored}, nil
		},
	}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if observeCalled {
		t.Fatal("Observe was called despite the mechanism check failing -- multi-turn observation must never run after a failed $0 wiring check")
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Violated record (mechanism failure fails safe)", report.Records)
	}
}

// TestRun_AgenticMechanismErrorFailsSafe proves a mechanism check that errors (rather than
// returning a Violated ClassifierResult) is still graded Violated, never left unresolved or
// mistaken for a pass -- the fail-safe bias applies to a harness-level error too, not only to a
// classifier's own negative verdict.
func TestRun_AgenticMechanismErrorFailsSafe(t *testing.T) {
	observeCalled := false
	opts := baseOptions(t)
	opts.Live = false
	opts.Cases = []Case{{
		ID:   "case-agentic",
		Kind: KindAgentic,
		Mechanism: func() (ClassifierResult, error) {
			return ClassifierResult{}, errors.New("mechanism check blew up")
		},
		Observe: func(transcript.TranscriptSource, string, string, string) (ClassifierResult, error) {
			observeCalled = true
			return ClassifierResult{Outcome: Honored}, nil
		},
	}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if observeCalled {
		t.Fatal("Observe was called despite the mechanism check erroring")
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Violated record (mechanism error fails safe)", report.Records)
	}
}

// TestRun_AgenticObserveErrorFailsSafe proves a transcript-observation error (a corrupt or
// unreadable session file) is graded Violated rather than silently skipped or treated as a pass.
func TestRun_AgenticObserveErrorFailsSafe(t *testing.T) {
	opts := baseOptions(t)
	opts.Live = false
	opts.Cases = []Case{{
		ID:        "case-agentic",
		Kind:      KindAgentic,
		Mechanism: FileExists(mustTempFile(t)),
		Observe: func(transcript.TranscriptSource, string, string, string) (ClassifierResult, error) {
			return ClassifierResult{}, errors.New("transcript unreadable")
		},
	}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Violated record (observe error fails safe)", report.Records)
	}
}

// TestProjectSlug_ReplacesNonIdentifierCharsDeterministically locks in the on-disk slug mapping a
// KindProbe trial's transcript lookup depends on: every non letter/digit/hyphen character in an
// absolute, symlink-resolved path becomes a hyphen, and the mapping is stable across calls.
func TestProjectSlug_ReplacesNonIdentifierCharsDeterministically(t *testing.T) {
	dir := t.TempDir()
	got1 := ProjectSlug(dir)
	got2 := ProjectSlug(dir)
	if got1 != got2 {
		t.Fatalf("ProjectSlug(%q) is not deterministic: %q vs %q", dir, got1, got2)
	}
	for _, r := range got1 {
		isID := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-'
		if !isID {
			t.Fatalf("ProjectSlug(%q) = %q contains non identifier/hyphen rune %q", dir, got1, r)
		}
	}
}

// TestEstimateTotalBudget_ZeroInputsYieldZero proves the derived-ceiling formula's degenerate
// inputs are treated as "no budget", never silently divided/multiplied into a nonsensical
// negative or NaN ceiling that Options.effectiveTotalBudget could then treat as unlimited.
func TestEstimateTotalBudget_ZeroInputsYieldZero(t *testing.T) {
	cases := []struct {
		trials, models int
		perProbe       float64
	}{
		{0, 5, 1.0},
		{5, 0, 1.0},
		{5, 5, 0},
		{-1, 5, 1.0},
	}
	for _, c := range cases {
		if got := EstimateTotalBudget(c.trials, c.models, c.perProbe); got != 0 {
			t.Errorf("EstimateTotalBudget(%d, %d, %v) = %v, want 0", c.trials, c.models, c.perProbe, got)
		}
	}
}
