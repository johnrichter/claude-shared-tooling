package plugin_behavioral

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// None of these tests invoke a real `claude` binary or spend anything: every ProbeRunner,
// VersionResolver and transcript.TranscriptSource below is a fixture-backed fake. This mirrors
// characterize's own sanity-test discipline, and is load-bearing here specifically -- this
// package's whole purpose is a metered live matrix, so its own test suite proving the harness
// logic (budget math, the leakage lint, grading, the opt-in interlock) never itself runs live is
// the acceptance bar for this file.

func fakeProbeRunner(sessionID string, costUSD float64, result string) ProbeRunner {
	return func(_ context.Context, _ []string, _ sysops.Options) (*sysops.Result, error) {
		pr := probeResultJSON{SessionID: sessionID, TotalCostUSD: costUSD, Result: result}
		raw, err := json.Marshal(pr)
		if err != nil {
			return nil, err
		}
		return &sysops.Result{ExitCode: 0, Stdout: raw}, nil
	}
}

func fixedVersion(v string) VersionResolver {
	return func(context.Context) (string, error) { return v, nil }
}

func writeTrackedSettings(t *testing.T, dir string, enabled map[string]bool) string {
	t.Helper()
	path := filepath.Join(dir, "settings.json")
	raw, err := json.Marshal(map[string]any{"enabledPlugins": enabled})
	if err != nil {
		t.Fatalf("marshal settings fixture: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write settings fixture: %v", err)
	}
	return path
}

func baseOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		PluginKey:           "my-plugin@my-marketplace",
		TrackedSettingsPath: writeTrackedSettings(t, dir, map[string]bool{"my-plugin@my-marketplace": true}),
		CCVersion:           "2.1.217",
		ResolveCCVersion:    fixedVersion("2.1.217"),
		Models:              []string{"claude-sonnet-5"},
		TrialsPerModel:      1,
		Live:                true,
		PerTrialBudgetUSD:   1.00,
		RunID:               "test-run",
	}
}

// TestRun_RefusesLiveProbeWithoutOptIn is the opt-in interlock's own acceptance proof: a matrix
// selecting a KindProbe case with Live=false and no ambient env override is refused before
// anything is provisioned or the fake probe is ever invoked.
func TestRun_RefusesLiveProbeWithoutOptIn(t *testing.T) {
	called := false
	opts := baseOptions(t)
	opts.Live = false
	opts.Run = func(context.Context, []string, sysops.Options) (*sysops.Result, error) {
		called = true
		return nil, errors.New("must not be called")
	}
	opts.Cases = []Case{{
		ID:       "case-1",
		Kind:     KindProbe,
		Prompt:   "say hello",
		Classify: ToolInvoked("Bash"),
	}}

	_, err := Run(context.Background(), opts)
	var interlockErr *LiveOptInRequiredError
	if !errors.As(err, &interlockErr) {
		t.Fatalf("Run err = %v, want *LiveOptInRequiredError", err)
	}
	if called {
		t.Fatal("the probe ran despite no --live opt-in")
	}
}

// TestRun_AgenticCaseNeedsNoLiveOptIn proves the interlock is scoped to KindProbe: an all-agentic
// matrix runs with Live left false, since it launches no probe and spends nothing.
func TestRun_AgenticCaseNeedsNoLiveOptIn(t *testing.T) {
	opts := baseOptions(t)
	opts.Live = false
	opts.Cases = []Case{{
		ID:        "case-agentic",
		Kind:      KindAgentic,
		Mechanism: FileExists(mustTempFile(t)),
		Observe: func(transcript.TranscriptSource, string, string, string) (ClassifierResult, error) {
			return ClassifierResult{Outcome: Honored, Evidence: "dispatch observed"}, nil
		},
	}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Honored {
		t.Fatalf("Records = %+v, want one Honored agentic record", report.Records)
	}
}

// TestRun_CalibrationBlockedShortCircuits proves Options.Calibration is checked before anything
// else -- interlock, env-fidelity, budget -- none of which run when it is set.
func TestRun_CalibrationBlockedShortCircuits(t *testing.T) {
	opts := baseOptions(t)
	opts.TrackedSettingsPath = "/does/not/exist"
	blocked := errors.New("phase 2 calibration blocked")
	opts.Calibration = blocked

	_, err := Run(context.Background(), opts)
	if !errors.Is(err, blocked) {
		t.Fatalf("Run err = %v, want the calibration error returned verbatim", err)
	}
}

// TestRun_EnvironmentFidelityFailsLoudWhenPluginInactive is the environment-fidelity acceptance
// proof: a plugin key absent from the tracked settings' enabledPlugins map fails loud rather than
// silently grading a trial against an inactive plugin.
func TestRun_EnvironmentFidelityFailsLoudWhenPluginInactive(t *testing.T) {
	opts := baseOptions(t)
	opts.TrackedSettingsPath = writeTrackedSettings(t, t.TempDir(), map[string]bool{"other-plugin@mp": true})
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	_, err := Run(context.Background(), opts)
	var inactiveErr *PluginInactiveError
	if !errors.As(err, &inactiveErr) {
		t.Fatalf("Run err = %v, want *PluginInactiveError", err)
	}
}

// TestRun_CCVersionMismatchFailsLoud pins the Claude Code version under test: an observed
// version that does not match Options.CCVersion fails loud rather than grading against an
// unconfirmed release.
func TestRun_CCVersionMismatchFailsLoud(t *testing.T) {
	opts := baseOptions(t)
	opts.ResolveCCVersion = fixedVersion("2.9.999")
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	_, err := Run(context.Background(), opts)
	var mismatchErr *CCVersionMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("Run err = %v, want *CCVersionMismatchError", err)
	}
}

// TestRun_LeakyPromptFailsWithoutRunning is the leakage-lint acceptance proof (a planted leaky
// case): a case whose own prompt names its forbidden term is graded Violated and never reaches
// the probe runner.
func TestRun_LeakyPromptFailsWithoutRunning(t *testing.T) {
	called := false
	opts := baseOptions(t)
	opts.Run = func(context.Context, []string, sysops.Options) (*sysops.Result, error) {
		called = true
		return nil, errors.New("must not be called")
	}
	opts.Cases = []Case{{
		ID:             "leaky-case",
		Kind:           KindProbe,
		Prompt:         "please use the daily-briefing skill right now",
		ForbiddenTerms: []string{"daily-briefing"},
		Classify:       ToolInvoked("Bash"),
	}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Fatal("the probe ran despite a leaky prompt")
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Violated record (leakage lint)", report.Records)
	}
}

// TestRun_InconclusivePositiveCaseFailsSafe is the inconclusive-to-fail acceptance proof: a
// positive case whose probe never elicited the tool under test is graded Violated, never a
// vacuous pass.
func TestRun_InconclusivePositiveCaseFailsSafe(t *testing.T) {
	opts := baseOptions(t)
	opts.Run = fakeProbeRunner("sess-1", 0.01, "done")
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Violated record (inconclusive positive case fails safe)", report.Records)
	}
}

// TestRun_InconclusiveNegativeCasePasses proves the one documented exception: a case declaring
// GuardsAgainstOverTriggering treats the same inconclusive outcome as a pass.
func TestRun_InconclusiveNegativeCasePasses(t *testing.T) {
	opts := baseOptions(t)
	opts.Run = fakeProbeRunner("sess-1", 0.01, "done")
	opts.Cases = []Case{{
		ID: "case-1", Kind: KindProbe, Prompt: "hi",
		GuardsAgainstOverTriggering: true,
		Classify:                    ToolInvoked("Bash"),
	}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || report.Records[0].Outcome != Honored {
		t.Fatalf("Records = %+v, want one Honored record (negative case, inconclusive passes)", report.Records)
	}
}

// TestRun_PerTrialBudgetAborts is the per-trial-abort acceptance proof: one trial's own real
// spend crossing its per-trial ceiling is graded Violated and flagged Aborted, independent of the
// matrix-wide total.
func TestRun_PerTrialBudgetAborts(t *testing.T) {
	opts := baseOptions(t)
	opts.PerTrialBudgetUSD = 0.10
	opts.TotalBudgetUSD = 100.00 // generous total -- isolates the per-trial check
	opts.Run = fakeProbeRunner("sess-1", 5.00, "done")
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || !report.Records[0].Aborted || report.Records[0].Outcome != Violated {
		t.Fatalf("Records = %+v, want one Aborted/Violated record", report.Records)
	}
	if report.Aborted {
		t.Fatal("Report.Aborted should reflect the matrix-wide ceiling, not a single trial's own abort")
	}
}

// TestRun_HardTotalCeilingAbortsMatrix is the hard-total-ceiling acceptance proof: two trials
// whose combined spend crosses TotalBudgetUSD stop the matrix before a third would run.
func TestRun_HardTotalCeilingAbortsMatrix(t *testing.T) {
	opts := baseOptions(t)
	opts.PerTrialBudgetUSD = 1.00
	opts.TotalBudgetUSD = 0.15
	opts.TrialsPerModel = 3
	calls := 0
	opts.Run = func(ctx context.Context, args []string, sysOpts sysops.Options) (*sysops.Result, error) {
		calls++
		return fakeProbeRunner("sess", 0.10, "done")(ctx, args, sysOpts)
	}
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Aborted {
		t.Fatalf("Report.Aborted = false, want true (0.10 + 0.10 > 0.15 ceiling)")
	}
	if calls != 2 {
		t.Fatalf("probe called %d times, want exactly 2 (the matrix must abort before a third trial)", calls)
	}
}

// TestRun_DefaultTotalBudgetDerivedFromFormula proves EstimateTotalBudget's own formula (trials x
// models x per-probe-cost) is what Run falls back to when TotalBudgetUSD is left at zero.
func TestRun_DefaultTotalBudgetDerivedFromFormula(t *testing.T) {
	opts := baseOptions(t)
	opts.TrialsPerModel = 2
	opts.Models = []string{"claude-sonnet-5", "claude-haiku-4-5"}
	opts.PerTrialBudgetUSD = 0.05 // formula ceiling = 2 * 2 * 0.05 = 0.20
	opts.Run = fakeProbeRunner("sess", 0.05, "done")
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Four trials at $0.05 each = $0.20 cumulative; the 4th crosses the $0.20 ceiling only if
	// strictly greater, so this run should complete all four without aborting.
	if report.Aborted {
		t.Fatalf("Report = %+v, want the derived $0.20 ceiling to exactly cover 4 trials at $0.05", report)
	}
	if len(report.Records) != 4 {
		t.Fatalf("Records = %d, want 4 (2 trials x 2 models)", len(report.Records))
	}
}

// TestRun_ModelPinOverridesRequestedTier checks the ambient model-pin override end to end: the
// resolved model recorded on every record is the pin, not the requested tier.
func TestRun_ModelPinOverridesRequestedTier(t *testing.T) {
	t.Setenv(ModelPinEnv, "claude-haiku-4-5")
	opts := baseOptions(t)
	opts.Models = []string{"claude-sonnet-5"}
	opts.Run = fakeProbeRunner("sess", 0.01, "done")
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Records) != 1 || report.Records[0].Model != "claude-haiku-4-5" {
		t.Fatalf("Records = %+v, want model claude-haiku-4-5 (the ambient pin)", report.Records)
	}
}

// TestRun_CoverageReportsManifestMinusExecuted is the coverage acceptance proof: a manifest case
// id never executed by this run's cases surfaces in MissingCoverage.
func TestRun_CoverageReportsManifestMinusExecuted(t *testing.T) {
	opts := baseOptions(t)
	opts.Run = fakeProbeRunner("sess", 0.01, "done")
	opts.ManifestCaseIDs = []string{"case-1", "case-2", "case-3"}
	opts.Cases = []Case{{ID: "case-1", Kind: KindProbe, Prompt: "hi", Classify: ToolInvoked("Bash")}}

	report, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := report.MissingCoverage; len(got) != 2 || got[0] != "case-2" || got[1] != "case-3" {
		t.Fatalf("MissingCoverage = %v, want [case-2 case-3]", got)
	}
}

// TestLintPrompt_IdentifierBoundaryCatchesHyphenatedTermWithoutFalsePositive proves the
// leakage-lint boundary rule directly: a hyphenated term matches as one token, and an unrelated
// word sharing a short substring is never flagged.
func TestLintPrompt_IdentifierBoundaryCatchesHyphenatedTermWithoutFalsePositive(t *testing.T) {
	findings := LintPrompt("Please invoke the daily-briefing skill for me.", []string{"daily-briefing"})
	if len(findings) != 1 || findings[0].Term != "daily-briefing" {
		t.Fatalf("findings = %+v, want exactly one daily-briefing finding", findings)
	}

	noFalsePositive := LintPrompt("Run a quick digit check on the input.", []string{"git"})
	if len(noFalsePositive) != 0 {
		t.Fatalf("findings = %+v, want none (\"digit\" must not match forbidden term \"git\")", noFalsePositive)
	}
}

// TestGradeInconclusive_PositiveVsNegative locks in the fail-safe bias directly, independent of
// the matrix driver.
func TestGradeInconclusive_PositiveVsNegative(t *testing.T) {
	result := ClassifierResult{Outcome: Inconclusive, Evidence: "never observed"}
	if got := GradeInconclusive(result, true); got != Violated {
		t.Errorf("positive case: GradeInconclusive = %q, want violated", got)
	}
	if got := GradeInconclusive(result, false); got != Honored {
		t.Errorf("negative case: GradeInconclusive = %q, want honored", got)
	}
	if got := GradeInconclusive(ClassifierResult{Outcome: Honored}, true); got != Honored {
		t.Errorf("a non-inconclusive outcome must pass through unchanged, got %q", got)
	}
}

// TestThrowaway_ProvisionAndCleanupGuardedByMarker proves the throwaway property directly: the
// marker exists the instant the directory is provisioned, and Cleanup refuses to remove a
// directory whose marker has been tampered away.
func TestThrowaway_ProvisionAndCleanupGuardedByMarker(t *testing.T) {
	tw, err := Provision("run-1", nil)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !IsThrowaway(tw.Dir) {
		t.Fatalf("provisioned dir %q does not carry the marker", tw.Dir)
	}
	if err := AssertThrowaway(tw.Dir, "test"); err != nil {
		t.Fatalf("AssertThrowaway: %v", err)
	}

	if err := os.Remove(filepath.Join(tw.Dir, ThrowawayMarker)); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	if err := Cleanup(tw.Dir); err == nil {
		t.Fatal("Cleanup succeeded on a directory whose marker was removed -- the guard did not hold")
	}
	_ = os.RemoveAll(tw.Dir)
}

func mustTempFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wired.md")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	return path
}
