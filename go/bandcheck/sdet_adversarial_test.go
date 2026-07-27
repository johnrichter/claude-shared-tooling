package bandcheck

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/gate"
	"github.com/johnrichter/claude-shared-tooling/go/roster"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

// --- ProbeFiring: plant/probe/observe, adversarial paths -------------------

// fakeSource is a minimal transcript.TranscriptSource that replays a canned turn set
// regardless of what bytes it is handed, so ProbeFiring's observe wiring can be exercised
// without a real transcript parser or a live `claude` binary.
type fakeSource struct {
	turns []transcript.Turn
	err   error
}

func (f fakeSource) ResolvePath(root, scope, sessionID string) string { return "" }

func (f fakeSource) DiscoverSubagentTranscripts(path string) ([]string, error) { return nil, nil }

func (f fakeSource) Turns(r io.Reader, fn func(transcript.Turn) error) error {
	if f.err != nil {
		return f.err
	}
	for _, t := range f.turns {
		if err := fn(t); err != nil {
			return err
		}
	}
	return nil
}

func TestProbeFiring_PlantsFileAndObservesFired(t *testing.T) {
	dir := t.TempDir()
	plantPath := filepath.Join(dir, "planted.txt")
	transcriptPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcriptPath, []byte("irrelevant, fakeSource ignores bytes\n"), 0o644); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}

	var ranName string
	var ranArgs []string
	runner := func(ctx context.Context, name string, args []string, opts sysops.Options) (*sysops.Result, error) {
		ranName, ranArgs = name, args
		// The probe run must see the planted artifact on disk before it runs.
		if _, err := os.Stat(plantPath); err != nil {
			t.Errorf("planted file not on disk when runner invoked: %v", err)
		}
		return &sysops.Result{ExitCode: 0, Stdout: []byte("ok")}, nil
	}

	src := fakeSource{turns: []transcript.Turn{{LineNo: 1, Authorship: transcript.AuthorOrchestrator}}}
	observeCalled := false
	obs, err := ProbeFiring(context.Background(), runner, src, "gate-x",
		Planted{Path: plantPath, Content: []byte("bait")},
		"probe prompt", transcriptPath,
		func(turns []transcript.Turn) (bool, string) {
			observeCalled = true
			if len(turns) != 1 {
				t.Errorf("observe got %d turns, want 1", len(turns))
			}
			return true, "matched"
		})
	if err != nil {
		t.Fatalf("ProbeFiring: %v", err)
	}
	if ranName != "claude" || len(ranArgs) != 2 || ranArgs[0] != "-p" || ranArgs[1] != "probe prompt" {
		t.Fatalf("got runner invocation name=%q args=%v, want claude -p <prompt>", ranName, ranArgs)
	}
	if !observeCalled {
		t.Fatal("observe callback never invoked")
	}
	if !obs.Detected || !obs.Fired || obs.Reason != "matched" || obs.GateID != "gate-x" {
		t.Fatalf("got %+v, want Detected=true Fired=true Reason=matched GateID=gate-x", obs)
	}
	if got, err := os.ReadFile(plantPath); err != nil || string(got) != "bait" {
		t.Fatalf("planted content = %q, err=%v, want %q", got, err, "bait")
	}
}

func TestProbeFiring_CreatesMissingPlantDirectories(t *testing.T) {
	dir := t.TempDir()
	plantPath := filepath.Join(dir, "nested", "deeper", "planted.txt")
	transcriptPath := filepath.Join(dir, "session.jsonl")
	os.WriteFile(transcriptPath, []byte("x\n"), 0o644)

	runner := func(ctx context.Context, name string, args []string, opts sysops.Options) (*sysops.Result, error) {
		return &sysops.Result{}, nil
	}
	src := fakeSource{}
	_, err := ProbeFiring(context.Background(), runner, src, "g", Planted{Path: plantPath, Content: []byte("x")},
		"p", transcriptPath, func(turns []transcript.Turn) (bool, string) { return false, "" })
	if err != nil {
		t.Fatalf("ProbeFiring: %v", err)
	}
	if _, err := os.Stat(plantPath); err != nil {
		t.Fatalf("plant not created under nested dirs: %v", err)
	}
}

func TestProbeFiring_RunnerErrorPropagatesBeforeObserve(t *testing.T) {
	dir := t.TempDir()
	plantPath := filepath.Join(dir, "planted.txt")
	runErr := context.DeadlineExceeded
	runner := func(ctx context.Context, name string, args []string, opts sysops.Options) (*sysops.Result, error) {
		return nil, runErr
	}
	observeCalled := false
	_, err := ProbeFiring(context.Background(), runner, fakeSource{}, "g", Planted{Path: plantPath, Content: []byte("x")},
		"p", filepath.Join(dir, "nope.jsonl"),
		func(turns []transcript.Turn) (bool, string) { observeCalled = true; return true, "" })
	if err == nil {
		t.Fatal("got nil error, want the runner's own error surfaced")
	}
	if observeCalled {
		t.Fatal("observe was called despite the run itself failing -- must never happen")
	}
}

func TestProbeFiring_MissingTranscriptIsUndetectedNotFired(t *testing.T) {
	dir := t.TempDir()
	plantPath := filepath.Join(dir, "planted.txt")
	runner := func(ctx context.Context, name string, args []string, opts sysops.Options) (*sysops.Result, error) {
		return &sysops.Result{}, nil
	}
	observeCalled := false
	obs, err := ProbeFiring(context.Background(), runner, fakeSource{}, "g", Planted{Path: plantPath, Content: []byte("x")},
		"p", filepath.Join(dir, "does-not-exist.jsonl"),
		func(turns []transcript.Turn) (bool, string) { observeCalled = true; return true, "" })
	if err != nil {
		t.Fatalf("ProbeFiring: %v", err)
	}
	if observeCalled {
		t.Fatal("observe was called despite an unreadable transcript -- must report undetected instead")
	}
	if obs.Detected || obs.Fired {
		t.Fatalf("got Detected=%v Fired=%v, want both false for an unreadable transcript", obs.Detected, obs.Fired)
	}
	if obs.Reason == "" {
		t.Fatal("got empty Reason for an undetected outcome, want an explanation")
	}
}

func TestProbeFiring_TurnsStreamErrorIsUndetectedNotFired(t *testing.T) {
	dir := t.TempDir()
	plantPath := filepath.Join(dir, "planted.txt")
	transcriptPath := filepath.Join(dir, "session.jsonl")
	os.WriteFile(transcriptPath, []byte("x\n"), 0o644)

	runner := func(ctx context.Context, name string, args []string, opts sysops.Options) (*sysops.Result, error) {
		return &sysops.Result{}, nil
	}
	src := fakeSource{err: io.ErrUnexpectedEOF}
	observeCalled := false
	obs, err := ProbeFiring(context.Background(), runner, src, "g", Planted{Path: plantPath, Content: []byte("x")},
		"p", transcriptPath, func(turns []transcript.Turn) (bool, string) { observeCalled = true; return true, "" })
	if err != nil {
		t.Fatalf("ProbeFiring: %v", err)
	}
	if observeCalled || obs.Detected || obs.Fired {
		t.Fatalf("got observeCalled=%v obs=%+v, want undetected on a stream failure", observeCalled, obs)
	}
}

func TestSysopsRunner_DelegatesToSysopsRun(t *testing.T) {
	// SysopsRunner is a thin adapter; the meaningful check is that it is assignable to
	// CommandRunner (compile-time) and forwards to a real subprocess. `true` always
	// succeeds without needing `claude` on the test host.
	var runner CommandRunner = SysopsRunner
	res, err := runner(context.Background(), "true", nil, sysops.Options{})
	if err != nil {
		t.Fatalf("SysopsRunner(true): %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("got exit code %d, want 0", res.ExitCode)
	}
}

// --- ParseSettingsEffort -----------------------------------------------------

func TestParseSettingsEffort_ReadsAndLowercasesField(t *testing.T) {
	effort, ok := ParseSettingsEffort([]byte(`{"effortLevel":"HIGH"}`))
	if !ok || effort != roster.EffortHigh {
		t.Fatalf("got effort=%q ok=%v, want %q/true", effort, ok, roster.EffortHigh)
	}
}

func TestParseSettingsEffort_AbsentFieldIsNotOK(t *testing.T) {
	if _, ok := ParseSettingsEffort([]byte(`{"otherField":"x"}`)); ok {
		t.Fatal("got ok=true for JSON with no effortLevel field")
	}
}

func TestParseSettingsEffort_EmptyStringIsNotOK(t *testing.T) {
	if _, ok := ParseSettingsEffort([]byte(`{"effortLevel":""}`)); ok {
		t.Fatal("got ok=true for an empty effortLevel string")
	}
}

func TestParseSettingsEffort_MalformedJSONIsNotOK(t *testing.T) {
	if _, ok := ParseSettingsEffort([]byte(`not json at all`)); ok {
		t.Fatal("got ok=true for unparseable JSON")
	}
}

// --- DetectSessionModel: no orchestrator turn anywhere means truly undetected --------

// TestDetectSessionModel_NoOrchestratorTurnAtAllIsUndetected is the true "undetected" case
// AC7 names: every line in the stream is either subagent-authored or carries no authorship
// marker (the FB13 inlining scenario with no leading, correctly-marked orchestrator turn to
// fall back on). ok must be false, and no subagent model may ever surface as a substitute.
func TestDetectSessionModel_NoOrchestratorTurnAtAllIsUndetected(t *testing.T) {
	inlined := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-8"}}
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","model":"claude-haiku-4-5"}}
`
	f := writeTemp(t, inlined)
	model, ok, err := DetectSessionModel(transcript.ClaudeCodeJSONL{}, f)
	if err != nil {
		t.Fatalf("DetectSessionModel: %v", err)
	}
	if ok || model != "" {
		t.Fatalf("got model=%q ok=%v, want ok=false and no model (no orchestrator-authored line present)", model, ok)
	}
}

// --- CheckRegistryFiring: doc-claimed Unparsed-affects-Clean invariant -----------------

// TestCheckRegistryFiring_UnparsedAloneIsNotClean pins the false-clean-bill guard: a shipped
// entry with an unparseable (prose) trigger and NO misses/undeclared firings is a declaration
// this check verified nothing about, so Clean() must be false even though Misses and Undeclared
// are empty. A caller reading only Clean() must never treat a registry with a checked-nothing
// entry as fully verified.
func TestCheckRegistryFiring_UnparsedAloneIsNotClean(t *testing.T) {
	declared := []gate.Rung2Declaration{
		{ID: "e1", Status: "shipped", GateID: "g1", Trigger: "PreToolUse:Bash on any command string, matched in-script"},
	}
	report, err := CheckRegistryFiring(declared, nil)
	if err != nil {
		t.Fatalf("CheckRegistryFiring: %v", err)
	}
	if len(report.Unparsed) != 1 || report.Unparsed[0] != "e1" {
		t.Fatalf("got Unparsed=%v, want [e1]", report.Unparsed)
	}
	if len(report.Misses) != 0 || len(report.Undeclared) != 0 {
		t.Fatalf("got Misses=%v Undeclared=%v, want both empty for this fixture", report.Misses, report.Undeclared)
	}
	if report.Clean() {
		t.Fatal("got Clean()==true with a non-empty Unparsed entry -- a checked-nothing entry must never pass as a clean bill")
	}
}

func TestCheckRegistryFiring_PlannedAndRetiredEntriesNeverChecked(t *testing.T) {
	declared := []gate.Rung2Declaration{
		{ID: "planned-1", Status: "planned", GateID: "gp", Trigger: "PreToolUse:Write on **/never/fired.md"},
		{ID: "retired-1", Status: "retired", GateID: "gr", Trigger: "PreToolUse:Write on **/never/fired.md"},
	}
	report, err := CheckRegistryFiring(declared, nil)
	if err != nil {
		t.Fatalf("CheckRegistryFiring: %v", err)
	}
	if !report.Clean() || len(report.Misses) != 0 {
		t.Fatalf("got %+v, want a clean report -- planned/retired entries are never a miss input", report)
	}
}

func TestCheckRegistryFiring_InvalidGlobErrors(t *testing.T) {
	declared := []gate.Rung2Declaration{
		{ID: "e1", Status: "shipped", GateID: "g1", Trigger: "PreToolUse:Write on [invalid"},
	}
	observed := []ObservedGateFiring{{GateID: "g1", Path: "x.go"}}
	if _, err := CheckRegistryFiring(declared, observed); err == nil {
		t.Fatal("got nil error for an invalid declared glob, want an error surfaced to the caller")
	}
}

// --- CheckOverfire: additional edge cases ----------------------------------------------

func TestCheckOverfire_UnconditionalRuleHasNoActualMatches(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("x"), 0o644)
	rule := Rule{Path: "unconditional.md", PathGlobs: nil, ScopeFound: true, ScopeTokens: []string{"a.md"}}
	report, err := CheckOverfire(rule, root)
	if err != nil {
		t.Fatalf("CheckOverfire: %v", err)
	}
	if len(report.ActualMatches) != 0 || len(report.Excess) != 0 || report.Precision != 1.0 {
		t.Fatalf("got %+v, want no actual matches and trivial precision for a paths-less rule", report)
	}
}

func TestCheckOverfire_ScopeFoundButNoUsableTokensFallsBackToGlobItself(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("x"), 0o644)
	rule := Rule{Path: "prose-scope.md", PathGlobs: []string{"*.md"}, ScopeFound: true, ScopeTokens: nil}
	report, err := CheckOverfire(rule, root)
	if err != nil {
		t.Fatalf("CheckOverfire: %v", err)
	}
	if !report.Clean() || report.Precision != 1.0 {
		t.Fatalf("got %+v, want trivially clean when the Scope paragraph names no usable token", report)
	}
}

func TestParseRule_NoScopeParagraphAtAll(t *testing.T) {
	text := []byte("---\npaths:\n  - \"**/*.go\"\n---\n# A rule\n\nNo scope statement here at all.\n")
	r, err := ParseRule("no-scope.md", text)
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if r.ScopeFound {
		t.Fatal("got ScopeFound=true, want false -- no **Scope.** paragraph present")
	}
}

// TestDetectSessionModel_NestedToolInputModelStringNeverLeaks pins AC6's non-recursion clause
// against the real ClaudeCodeJSONL parser (not the mocked fakeSource): an orchestrator turn
// whose tool_use input happens to contain a string keyed "model" must never surface as the
// detected tier -- only ccLine/ccNested's own flat Model field, as the transcript package
// itself parses it, is ever consulted.
func TestDetectSessionModel_NestedToolInputModelStringNeverLeaks(t *testing.T) {
	inlined := `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"model":"claude-opus-4-8","command":"echo hi"}}]}}
`
	f := writeTemp(t, inlined)
	model, ok, err := DetectSessionModel(transcript.ClaudeCodeJSONL{}, f)
	if err != nil {
		t.Fatalf("DetectSessionModel: %v", err)
	}
	if ok || model != "" {
		t.Fatalf("got model=%q ok=%v, want undetected -- a tool_use input's nested \"model\" string must never surface as the session tier", model, ok)
	}
}

// TestDetectSessionModel_MalformedLineNeverSurfacesAModel guards against a malformed JSONL line
// (Malformed=true) contributing a model value even if some stray bytes happen to parse a
// top-level Model field before the line as a whole fails structurally.
func TestDetectSessionModel_MalformedLineNeverSurfacesAModel(t *testing.T) {
	inlined := "not json at all\n" +
		`{"type":"assistant","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5"}}` + "\n"
	f := writeTemp(t, inlined)
	model, ok, err := DetectSessionModel(transcript.ClaudeCodeJSONL{}, f)
	if err != nil {
		t.Fatalf("DetectSessionModel: %v", err)
	}
	if !ok || model != "claude-sonnet-5" {
		t.Fatalf("got model=%q ok=%v, want claude-sonnet-5 -- the malformed line must be skipped, not poison the scan", model, ok)
	}
}

// TestCheckOverfire_TrailingDoubleStarWidenedNotDirectoryMatch pins expandGlobs' documented
// widening of a bare trailing "**" to "**/*": a directory match must never surface as a
// reportable actual match, only the real files under it.
func TestCheckOverfire_TrailingDoubleStarWidenedNotDirectoryMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWrite(t, filepath.Join(root, "sub", "a.md"), "x")
	mustWrite(t, filepath.Join(root, "sub", "nested", "b.md"), "x")

	rule := Rule{Path: "trailing.md", PathGlobs: []string{"sub/**"}, ScopeFound: true, ScopeTokens: []string{"sub/**"}}
	report, err := CheckOverfire(rule, root)
	if err != nil {
		t.Fatalf("CheckOverfire: %v", err)
	}
	for _, m := range report.ActualMatches {
		if m == "sub" || m == "sub/nested" {
			t.Fatalf("got directory %q in ActualMatches=%v, want only files", m, report.ActualMatches)
		}
	}
	if len(report.ActualMatches) != 2 {
		t.Fatalf("got ActualMatches=%v, want the 2 real files under sub/", report.ActualMatches)
	}
}

// TestCheckRegistryFiring_SameGateTwoDeclaredEntriesBothMustFire pins that a fired glob only
// clears its OWN entry's miss, not a sibling entry sharing the same GateID -- CheckRegistryFiring
// tracks fired-ness per (GateID, glob) pair, not merely per GateID, so a gate declared twice for
// two distinct globs cannot pass on the strength of only one of them firing.
func TestCheckRegistryFiring_SameGateTwoDeclaredEntriesBothMustFire(t *testing.T) {
	declared := []gate.Rung2Declaration{
		{ID: "e1", Status: "shipped", GateID: "g1", Trigger: "PreToolUse:Write on **/agents/**"},
		{ID: "e2", Status: "shipped", GateID: "g1", Trigger: "PreToolUse:Write on **/skills/**"},
	}
	observed := []ObservedGateFiring{{GateID: "g1", Path: "agents/foo.md"}}

	report, err := CheckRegistryFiring(declared, observed)
	if err != nil {
		t.Fatalf("CheckRegistryFiring: %v", err)
	}
	if report.Clean() {
		t.Fatalf("got Clean()==true, want a miss on e2's undischarged glob: %+v", report)
	}
	if len(report.Misses) != 1 || report.Misses[0].EntryID != "e2" {
		t.Fatalf("got misses=%+v, want exactly one miss on e2's skills/** glob", report.Misses)
	}
}

// TestSelfCheck_EqualToFloorAndCeilingIsSilent pins the inclusive-both-ends contract on the
// exact boundary values, not just comfortably inside/outside the band.
func TestSelfCheck_EqualToFloorAndCeilingIsSilent(t *testing.T) {
	band := TierBand{
		FloorModel: "claude-sonnet-5", FloorEffort: roster.EffortMedium,
		CeilingModel: "claude-sonnet-5", CeilingEffort: roster.EffortMedium,
	}
	r := SelfCheck("claude-sonnet-5", roster.EffortMedium, true, band)
	if r.RosterStale || r.Verdict != gate.VerdictSilent {
		t.Fatalf("got %+v, want silent at the exact floor==ceiling boundary", r)
	}
}

// TestSelfCheck_EffortUndetectedUnknownModelIsRosterStale exercises the model-only comparison
// branch's own roster-stale exit (effortDetected=false), distinct from the effort-aware branch
// TestSelfCheck_UnknownModelIsRosterStale already covers.
func TestSelfCheck_EffortUndetectedUnknownModelIsRosterStale(t *testing.T) {
	r := SelfCheck("claude-not-a-real-model", "", false, buildBand)
	if !r.RosterStale {
		t.Fatalf("got %+v, want RosterStale (model-only branch, unknown model)", r)
	}
}

// TestSelfCheck_EffortUndetectedModelInBandIsSilentWithReason pins the model-only Reason text
// distinguishing "in band, effort skipped" from the effort-aware "within band" default.
func TestSelfCheck_EffortUndetectedModelInBandIsSilentWithReason(t *testing.T) {
	r := SelfCheck("claude-sonnet-5", "", false, buildBand)
	if r.RosterStale || r.Verdict != gate.VerdictSilent {
		t.Fatalf("got %+v, want silent", r)
	}
	if r.Reason == "" || r.Reason == "within band" {
		t.Fatalf("got Reason=%q, want the model-only effort-undetectable note", r.Reason)
	}
}

func TestParseRule_NoFrontmatterIsUnconditional(t *testing.T) {
	text := []byte("# A rule with no frontmatter\n\n**Scope.** `some.md` only.\n")
	r, err := ParseRule("no-fm.md", text)
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if len(r.PathGlobs) != 0 {
		t.Fatalf("got PathGlobs=%v, want empty for a rule with no paths: frontmatter", r.PathGlobs)
	}
	if !r.ScopeFound || len(r.ScopeTokens) != 1 || r.ScopeTokens[0] != "**/some.md" {
		t.Fatalf("got ScopeFound=%v ScopeTokens=%v, want [**/some.md]", r.ScopeFound, r.ScopeTokens)
	}
}
