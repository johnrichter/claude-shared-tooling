package bandcheck

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/gate"
	"github.com/johnrichter/claude-shared-tooling/go/roster"
	"github.com/johnrichter/claude-shared-tooling/go/transcript"
)

var buildBand = TierBand{
	FloorModel: "claude-sonnet-5", FloorEffort: roster.EffortMedium,
	CeilingModel: "claude-sonnet-5", CeilingEffort: roster.EffortHigh,
}

func TestSelfCheck_WithinBandIsSilent(t *testing.T) {
	r := SelfCheck("claude-sonnet-5", roster.EffortHigh, true, buildBand)
	if r.RosterStale || r.Verdict != gate.VerdictSilent {
		t.Fatalf("got %+v", r)
	}
}

func TestSelfCheck_BelowFloorAborts(t *testing.T) {
	r := SelfCheck("claude-haiku-4-5", roster.EffortHigh, true, buildBand)
	if r.RosterStale || r.Verdict != gate.VerdictAbort {
		t.Fatalf("got %+v", r)
	}
}

func TestSelfCheck_AboveCeilingWarns(t *testing.T) {
	r := SelfCheck("claude-opus-4-8", roster.EffortHigh, true, buildBand)
	if r.RosterStale || r.Verdict != gate.VerdictWarn {
		t.Fatalf("got %+v", r)
	}
}

func TestSelfCheck_UnknownModelIsRosterStale(t *testing.T) {
	r := SelfCheck("claude-not-a-real-model", roster.EffortHigh, true, buildBand)
	if !r.RosterStale {
		t.Fatalf("got %+v, want RosterStale", r)
	}
}

// TestDetectSessionModel_SkipsSubagentAndUnknownLines exercises FB13's stated contract against
// the shared transcript fixture: the transcript's LAST line is a subagent turn naming a
// different model than the orchestrator's own last turn, and a middle line carries no
// authorship marker at all. Only the orchestrator-authored model must come back.
func TestDetectSessionModel_SkipsSubagentAndUnknownLines(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "transcript", "testdata", "session", "main.jsonl"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	model, ok, err := DetectSessionModel(transcript.ClaudeCodeJSONL{}, f)
	if err != nil {
		t.Fatalf("DetectSessionModel: %v", err)
	}
	if !ok || model != "claude-sonnet-5" {
		t.Fatalf("got model=%q ok=%v, want claude-sonnet-5 (the last ORCHESTRATOR turn, not the trailing subagent turn's claude-opus-4-8)", model, ok)
	}
}

// TestDetectSessionModel_InlinedSubagentTurnsAreUndetected simulates a future transcript format
// (or a harness that stopped separating subagent turns into their own files) that inlines a
// subagent turn into the main stream with no isSidechain marker at all. That turn resolves
// AuthorUnknown, never AuthorOrchestrator -- so its model must never surface here, even though it
// is the last line in the stream.
func TestDetectSessionModel_InlinedSubagentTurnsAreUndetected(t *testing.T) {
	inlined := `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","model":"claude-sonnet-5"}}
{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-8"}}
`
	f := writeTemp(t, inlined)
	model, ok, err := DetectSessionModel(transcript.ClaudeCodeJSONL{}, f)
	if err != nil {
		t.Fatalf("DetectSessionModel: %v", err)
	}
	if !ok || model != "claude-sonnet-5" {
		t.Fatalf("got model=%q ok=%v, want claude-sonnet-5 -- the unmarked inlined line must never resolve", model, ok)
	}
}

func writeTemp(t *testing.T, content string) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "transcript-*.jsonl")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek temp: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestCheckRegistryFiring_MissAndUndeclaredFail(t *testing.T) {
	declared := []gate.Rung2Declaration{
		{ID: "e1", Status: "shipped", GateID: "g1", Trigger: "PreToolUse:Write,Edit on **/.claude/agents/**, **/SKILL.md"},
		{ID: "e2", Status: "shipped", GateID: "g2", Trigger: "PreToolUse:Bash on any command string, matched in-script"},
	}
	observed := []ObservedGateFiring{
		{GateID: "g1", Path: ".claude/agents/foo.md"}, // matches a declared glob
		{GateID: "g3", Path: "some/other/path"},       // no declared entry at all -> undeclared
	}

	report, err := CheckRegistryFiring(declared, observed)
	if err != nil {
		t.Fatalf("CheckRegistryFiring: %v", err)
	}
	if report.Clean() {
		t.Fatalf("got Clean()==true, want misses+undeclared: %+v", report)
	}
	if len(report.Misses) != 1 || report.Misses[0].Glob != "**/SKILL.md" {
		t.Fatalf("got misses=%+v, want one miss on **/SKILL.md", report.Misses)
	}
	if len(report.Undeclared) != 1 || report.Undeclared[0].GateID != "g3" {
		t.Fatalf("got undeclared=%+v, want one undeclared firing on g3", report.Undeclared)
	}
	if len(report.Unparsed) != 1 || report.Unparsed[0] != "e2" {
		t.Fatalf("got unparsed=%+v, want e2 (prose trigger, no literal glob)", report.Unparsed)
	}
}

func TestCheckRegistryFiring_ExactMatchIsClean(t *testing.T) {
	declared := []gate.Rung2Declaration{
		{ID: "e1", Status: "shipped", GateID: "g1", Trigger: "PreToolUse:Write on **/*.go"},
	}
	observed := []ObservedGateFiring{{GateID: "g1", Path: "go/bandcheck/tier.go"}}

	report, err := CheckRegistryFiring(declared, observed)
	if err != nil {
		t.Fatalf("CheckRegistryFiring: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("got %+v, want clean", report)
	}
}

func TestCheckOverfire_AbsentScopeIsLoudNeverClean(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.md"), "x")
	mustWrite(t, filepath.Join(root, "b.md"), "x")

	rule := Rule{Path: "no-scope.md", PathGlobs: []string{"*.md"}, ScopeFound: false}
	report, err := CheckOverfire(rule, root)
	if err != nil {
		t.Fatalf("CheckOverfire: %v", err)
	}
	if report.ScopeFound {
		t.Fatalf("got ScopeFound=true, want false (no Scope paragraph)")
	}
	if report.ScopeAbsentWarning == "" {
		t.Fatalf("got empty ScopeAbsentWarning, want a loud notice")
	}
	if report.Clean() {
		t.Fatalf("got Clean()==true, want the absent-Scope case to never read as clean")
	}
}

func TestCheckOverfire_ExcessFlaggedWhenScopeNarrower(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.md"), "x")
	mustWrite(t, filepath.Join(root, "b.md"), "x")

	rule := Rule{Path: "scoped.md", PathGlobs: []string{"*.md"}, ScopeFound: true, ScopeTokens: []string{"a.md"}}
	report, err := CheckOverfire(rule, root)
	if err != nil {
		t.Fatalf("CheckOverfire: %v", err)
	}
	if len(report.Excess) != 1 || report.Excess[0] != "b.md" {
		t.Fatalf("got excess=%v, want [b.md]", report.Excess)
	}
	if report.Precision != 0.5 {
		t.Fatalf("got precision=%v, want 0.5", report.Precision)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestParseRule_FrontmatterAndScopeParagraph(t *testing.T) {
	text := []byte(`---
paths:
  - "**/.gitignore"
---
# A rule

**Invariant.** Something holds.
**Scope.** ` + "`.gitignore`" + ` files (path-scoped).
`)
	r, err := ParseRule("r.md", text)
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if len(r.PathGlobs) != 1 || r.PathGlobs[0] != "**/.gitignore" {
		t.Fatalf("got PathGlobs=%v", r.PathGlobs)
	}
	if !r.ScopeFound {
		t.Fatalf("got ScopeFound=false, want true")
	}
	if len(r.ScopeTokens) != 1 || r.ScopeTokens[0] != "**/.gitignore" {
		t.Fatalf("got ScopeTokens=%v, want [**/.gitignore] (bare filename widened)", r.ScopeTokens)
	}
}
