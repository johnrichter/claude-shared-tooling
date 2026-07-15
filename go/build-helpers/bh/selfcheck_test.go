package bh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- LatestTranscriptModel ----

func TestLatestTranscriptModel_NestedMessageWins(t *testing.T) {
	jsonl := `{"model":"claude-haiku-4-5"}
{"message":{"model":"claude-opus-4-8"}}
`
	m, ok := LatestTranscriptModel(strings.NewReader(jsonl))
	if !ok || m != ModelOpus48 {
		t.Fatalf("got (%q,%v), want (%q,true)", m, ok, ModelOpus48)
	}
}

func TestLatestTranscriptModel_LastLineWins(t *testing.T) {
	// A mid-session /model override: earlier lines name one model, the LAST line names another.
	jsonl := `{"message":{"model":"claude-sonnet-5"}}
{"message":{"model":"claude-opus-4-8"}}
{"message":{"model":"claude-sonnet-5"}}
`
	m, ok := LatestTranscriptModel(strings.NewReader(jsonl))
	if !ok || m != ModelSonnet5 {
		t.Fatalf("got (%q,%v), want (%q,true) -- last line must win, not first or highest", m, ok, ModelSonnet5)
	}
}

func TestLatestTranscriptModel_EmptyTranscript(t *testing.T) {
	m, ok := LatestTranscriptModel(strings.NewReader(""))
	if ok {
		t.Fatalf("got ok=true for empty transcript, want false (model=%q)", m)
	}
}

func TestLatestTranscriptModel_MalformedLinesSkipped(t *testing.T) {
	jsonl := `not json at all
{"message":{"model":"claude-opus-4-8"}}
{{{broken
`
	m, ok := LatestTranscriptModel(strings.NewReader(jsonl))
	if !ok || m != ModelOpus48 {
		t.Fatalf("got (%q,%v), want (%q,true) -- malformed lines must not abort the scan", m, ok, ModelOpus48)
	}
}

func TestLatestTranscriptModel_LineWithNoModelDoesNotClobberEarlierLine(t *testing.T) {
	// A trailing line that names no model at all (e.g. a tool_result line) must not erase the
	// last real model reading.
	jsonl := `{"message":{"model":"claude-opus-4-8"}}
{"message":{"role":"user"}}
`
	m, ok := LatestTranscriptModel(strings.NewReader(jsonl))
	if !ok || m != ModelOpus48 {
		t.Fatalf("got (%q,%v), want (%q,true)", m, ok, ModelOpus48)
	}
}

// ---- ParseSettingsEffort ----

func TestParseSettingsEffort_Present(t *testing.T) {
	e, ok := ParseSettingsEffort([]byte(`{"effortLevel":"High"}`))
	if !ok || e != EffortHigh {
		t.Fatalf("got (%q,%v), want (%q,true) -- must lowercase", e, ok, EffortHigh)
	}
}

func TestParseSettingsEffort_Absent(t *testing.T) {
	e, ok := ParseSettingsEffort([]byte(`{"other":"x"}`))
	if ok {
		t.Fatalf("got ok=true (effort=%q) for absent effortLevel, want false", e)
	}
}

func TestParseSettingsEffort_EmptyString(t *testing.T) {
	e, ok := ParseSettingsEffort([]byte(`{"effortLevel":""}`))
	if ok {
		t.Fatalf("got ok=true (effort=%q) for empty effortLevel, want false", e)
	}
}

func TestParseSettingsEffort_MalformedJSON(t *testing.T) {
	e, ok := ParseSettingsEffort([]byte(`not json`))
	if ok {
		t.Fatalf("got ok=true (effort=%q) for malformed JSON, want false", e)
	}
}

// ---- SelfCheck: model+effort band, effort detected ----

func band(floorM Model, floorE Effort, ceilM Model, ceilE Effort) TierBand {
	return TierBand{FloorModel: floorM, FloorEffort: floorE, CeilingModel: ceilM, CeilingEffort: ceilE}
}

func TestSelfCheck_BelowFloorModelAborts(t *testing.T) {
	b := band(ModelSonnet5, EffortMedium, ModelOpus48, EffortMax)
	r := SelfCheck(ModelHaiku45, EffortHigh, true, b)
	if !r.Abort {
		t.Fatalf("expected abort for model below floor, got %+v", r)
	}
}

func TestSelfCheck_BelowFloorEffortSameModelAborts(t *testing.T) {
	// Same model as floor, but effort below the floor's effort -- must still abort since
	// tierRank is a composite of both dimensions.
	b := band(ModelSonnet5, EffortHigh, ModelSonnet5, EffortMax)
	r := SelfCheck(ModelSonnet5, EffortLow, true, b)
	if !r.Abort {
		t.Fatalf("expected abort for effort below floor at same model, got %+v", r)
	}
}

func TestSelfCheck_AboveCeilingWarnsDoesNotAbort(t *testing.T) {
	b := band(ModelHaiku45, EffortLow, ModelSonnet5, EffortMedium)
	r := SelfCheck(ModelOpus48, EffortMax, true, b)
	if r.Abort {
		t.Fatalf("expected warn-only (no abort) above ceiling, got abort: %+v", r)
	}
	if len(r.Warnings) == 0 {
		t.Fatalf("expected a ceiling warning, got none: %+v", r)
	}
}

func TestSelfCheck_AtFloorInclusiveIsFine(t *testing.T) {
	b := band(ModelSonnet5, EffortMedium, ModelOpus48, EffortMax)
	r := SelfCheck(ModelSonnet5, EffortMedium, true, b)
	if r.Abort || len(r.Warnings) != 0 {
		t.Fatalf("floor is inclusive, expected no abort/warning, got %+v", r)
	}
}

func TestSelfCheck_AtCeilingInclusiveIsFine(t *testing.T) {
	b := band(ModelSonnet5, EffortMedium, ModelOpus48, EffortMax)
	r := SelfCheck(ModelOpus48, EffortMax, true, b)
	if r.Abort || len(r.Warnings) != 0 {
		t.Fatalf("ceiling is inclusive, expected no abort/warning, got %+v", r)
	}
}

func TestSelfCheck_InBandIsSilent(t *testing.T) {
	b := band(ModelHaiku45, EffortLow, ModelOpus48, EffortMax)
	r := SelfCheck(ModelSonnet5, EffortHigh, true, b)
	if r.Abort || len(r.Warnings) != 0 {
		t.Fatalf("expected silent pass in band, got %+v", r)
	}
}

func TestSelfCheck_CrossModelBandSpansCorrectly(t *testing.T) {
	// Band spans two different models (floor=Sonnet5/high, ceiling=Opus48/low). A model one rank
	// below Sonnet5 at max effort should still fail since model dominates the composite score.
	b := band(ModelSonnet5, EffortHigh, ModelOpus48, EffortLow)
	r := SelfCheck(ModelHaiku45, EffortMax, true, b)
	if !r.Abort {
		t.Fatalf("expected abort: weaker model at max effort must not out-rank a stronger floor model, got %+v", r)
	}
}

func TestSelfCheck_UnrecognizedModelBelowAnyFloor(t *testing.T) {
	b := band(ModelHaiku45, EffortLow, ModelOpus48, EffortMax)
	r := SelfCheck(Model("claude-mystery-9"), EffortHigh, true, b)
	if !r.Abort {
		t.Fatalf("unrecognized model must rank below any real floor (fail closed), got %+v", r)
	}
}

func TestSelfCheck_DateSuffixedTranscriptModelNormalizes(t *testing.T) {
	// Real transcript IDs carry a trailing -YYYYMMDD; must resolve to the same rank as the bare
	// pinned model, not be treated as unrecognized.
	b := band(ModelSonnet5, EffortMedium, ModelOpus48, EffortMax)
	r := SelfCheck(Model("claude-sonnet-5-20260101"), EffortHigh, true, b)
	if r.Abort {
		t.Fatalf("date-suffixed model id must normalize to its bare form before ranking, got abort: %+v", r)
	}
}

// ---- SelfCheck: effort undetectable ----

func TestSelfCheck_UndetectableEffort_ModelInBand_WarnsSkipsEffort(t *testing.T) {
	b := band(ModelHaiku45, EffortHigh, ModelOpus48, EffortLow)
	r := SelfCheck(ModelSonnet5, "", false, b)
	if r.Abort {
		t.Fatalf("model in band with undetectable effort must not abort, got %+v", r)
	}
	if len(r.Warnings) == 0 {
		t.Fatalf("expected a warning noting effort was skipped, got none: %+v", r)
	}
	if !r.EffortDetected == false {
		// sanity of the field itself
		t.Fatalf("EffortDetected should be false, got %+v", r)
	}
}

func TestSelfCheck_UndetectableEffort_ModelBelowFloorStillAborts(t *testing.T) {
	// Model dimension is enforced REGARDLESS of effort detectability.
	b := band(ModelSonnet5, EffortMedium, ModelOpus48, EffortMax)
	r := SelfCheck(ModelHaiku45, "", false, b)
	if !r.Abort {
		t.Fatalf("model band must still be enforced when effort is undetectable, got %+v", r)
	}
}

func TestSelfCheck_UndetectableEffort_ModelAboveCeilingWarnsNotAbort(t *testing.T) {
	b := band(ModelHaiku45, EffortLow, ModelSonnet46, EffortMax)
	r := SelfCheck(ModelOpus48, "", false, b)
	if r.Abort {
		t.Fatalf("model above ceiling with undetectable effort must warn, not abort, got %+v", r)
	}
	if len(r.Warnings) < 2 {
		// one warning for undetectable effort, one for above-ceiling model
		t.Fatalf("expected both the effort-skipped warning and the ceiling warning, got %+v", r.Warnings)
	}
}

func TestSelfCheck_UndetectableEffort_IgnoresBandEffortFields(t *testing.T) {
	// Floor/ceiling effort fields must be irrelevant when effort is undetectable -- only model
	// bounds matter. Use an extreme floor effort that would fail any effort-aware check.
	b := band(ModelHaiku45, EffortMax, ModelOpus48, EffortMax)
	r := SelfCheck(ModelHaiku45, "", false, b)
	if r.Abort {
		t.Fatalf("band effort fields must be ignored entirely when effort is undetectable, got %+v", r)
	}
}

// ---- SCf: deterministic transcript resolution + session-id identity guard ----

const (
	testUUIDOurs  = "bf987116-b9d9-44ca-a049-6253d4992531"
	testUUIDOther = "aaaaaaaa-1111-2222-3333-444444444444"
)

// ---- ValidSessionID ----

func TestValidSessionID_WellFormedUUID(t *testing.T) {
	if !ValidSessionID(testUUIDOurs) {
		t.Fatalf("expected %q to be a valid session id", testUUIDOurs)
	}
}

func TestValidSessionID_RejectsNonUUIDShapes(t *testing.T) {
	for _, bad := range []string{
		"",
		"not-a-uuid",
		"bf987116b9d944caa0496253d4992531",      // no dashes
		"bf987116-b9d9-44ca-a049-6253d499253",   // last group too short
		"bf987116-b9d9-44ca-a049-6253d49925311", // last group too long
		"gf987116-b9d9-44ca-a049-6253d4992531",  // non-hex char
	} {
		if ValidSessionID(bad) {
			t.Fatalf("expected %q to be rejected as an invalid session id", bad)
		}
	}
}

// ---- SlugifyCWD ----

func TestSlugifyCWD_ReplacesNonAlnumWithDash(t *testing.T) {
	got := SlugifyCWD("/home/user.name/projects/my-app")
	want := "-home-user-name-projects-my-app"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---- ParseScratchpadPath ----

func TestParseScratchpadPath_WellFormed(t *testing.T) {
	slug, id, ok := ParseScratchpadPath("/private/tmp/claude-502/-home-user-my-app/" + testUUIDOurs + "/scratchpad")
	if !ok {
		t.Fatalf("expected ok=true for a well-formed scratchpad path")
	}
	if slug != "-home-user-my-app" || id != testUUIDOurs {
		t.Fatalf("got slug=%q id=%q, want slug=%q id=%q", slug, id, "-home-user-my-app", testUUIDOurs)
	}
}

func TestParseScratchpadPath_NestedFileUnderScratchpadStillResolves(t *testing.T) {
	// A file INSIDE the scratchpad dir (not the bare "scratchpad" leaf itself) must still resolve
	// to the same (slug, id) -- the session-id/cwd-slug pair sits at fixed offsets before the
	// LAST "scratchpad" path component, regardless of what follows it.
	slug, id, ok := ParseScratchpadPath("/private/tmp/claude-502/-home-user-my-app/" + testUUIDOurs + "/scratchpad/notes/foo.txt")
	if !ok || slug != "-home-user-my-app" || id != testUUIDOurs {
		t.Fatalf("got (slug=%q,id=%q,ok=%v), want (-home-user-my-app,%s,true)", slug, id, ok, testUUIDOurs)
	}
}

func TestParseScratchpadPath_NoScratchpadComponent_Unparseable(t *testing.T) {
	_, _, ok := ParseScratchpadPath("/private/tmp/claude-502/-home-user-my-app/" + testUUIDOurs + "/notascratchpad")
	if ok {
		t.Fatalf("expected ok=false when no path component is literally \"scratchpad\"")
	}
}

func TestParseScratchpadPath_ScratchpadTooShallow_Unparseable(t *testing.T) {
	// "scratchpad" present but with fewer than two preceding components -- no room for a
	// session-id/cwd-slug pair.
	_, _, ok := ParseScratchpadPath("/" + testUUIDOurs + "/scratchpad")
	if ok {
		t.Fatalf("expected ok=false when scratchpad has fewer than two preceding path components")
	}
}

func TestParseScratchpadPath_PrecedingComponentNotAUUID_Unparseable(t *testing.T) {
	_, _, ok := ParseScratchpadPath("/private/tmp/claude-502/-home-user-my-app/not-a-session-id/scratchpad")
	if ok {
		t.Fatalf("expected ok=false when the component before \"scratchpad\" isn't a valid session id")
	}
}

// ---- ResolveTranscriptPath (+ the mtime-regression fixture) ----

func TestResolveTranscriptPath_IsAPureDeterministicJoin(t *testing.T) {
	got := ResolveTranscriptPath("/root", "slug", testUUIDOurs)
	want := filepath.Join("/root", "slug", testUUIDOurs+".jsonl")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestResolveTranscriptPath_NeverPicksNewerOtherSessionTranscript is the SCf regression fixture
// (M17.P1.T1 acceptance): a concurrently-newer OTHER session's transcript sits in the SAME
// cwd-derived directory as ours, with a strictly newer mtime. A mtime-newest-wins resolver (the
// pre-fix behavior this guards against) would pick the other session's file; ResolveTranscriptPath
// must resolve to OUR path regardless, because it never looks at either file's mtime (or even its
// existence) -- it is a pure id-keyed join. This test would FAIL against a reintroduced
// mtime-scan implementation of transcript resolution.
func TestResolveTranscriptPath_NeverPicksNewerOtherSessionTranscript(t *testing.T) {
	dir := t.TempDir()
	slugDir := filepath.Join(dir, "-home-user-my-app")
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ourPath := filepath.Join(slugDir, testUUIDOurs+".jsonl")
	otherPath := filepath.Join(slugDir, testUUIDOther+".jsonl")
	if err := os.WriteFile(ourPath, []byte(`{"sessionId":"`+testUUIDOurs+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte(`{"sessionId":"`+testUUIDOther+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force the OTHER session's file to be strictly newer than ours -- the exact condition an
	// mtime-newest scan would use to (wrongly) prefer it.
	now := time.Now()
	if err := os.Chtimes(ourPath, now.Add(-1*time.Hour), now.Add(-1*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(otherPath, now, now); err != nil {
		t.Fatal(err)
	}

	got := ResolveTranscriptPath(dir, "-home-user-my-app", testUUIDOurs)
	if got != ourPath {
		t.Fatalf("got %q, want our own transcript %q -- resolver must never select a differently-named file", got, ourPath)
	}

	// Prove it's genuinely OUR content, not merely a name coincidence.
	raw, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("resolved path unreadable: %v", err)
	}
	scan := LatestTranscriptSessionID(strings.NewReader(string(raw)))
	if scan.SessionID != testUUIDOurs {
		t.Fatalf("resolved transcript names sessionId %q, want %q", scan.SessionID, testUUIDOurs)
	}
}

// ---- LatestTranscriptSessionID ----

func TestLatestTranscriptSessionID_LastLineWins(t *testing.T) {
	jsonl := `{"sessionId":"` + testUUIDOther + `"}
{"sessionId":"` + testUUIDOurs + `"}
`
	scan := LatestTranscriptSessionID(strings.NewReader(jsonl))
	if !scan.Found() || scan.SessionID != testUUIDOurs {
		t.Fatalf("got %+v, want SessionID=%q", scan, testUUIDOurs)
	}
	if scan.Lines != 2 || scan.Parsed != 2 {
		t.Fatalf("got %+v, want Lines=2 Parsed=2", scan)
	}
}

func TestLatestTranscriptSessionID_EmptyTranscript(t *testing.T) {
	scan := LatestTranscriptSessionID(strings.NewReader(""))
	if scan.Lines != 0 {
		t.Fatalf("got Lines=%d, want 0 for an empty transcript", scan.Lines)
	}
	if scan.Found() {
		t.Fatalf("got %+v, expected Found()=false for an empty transcript", scan)
	}
}

func TestLatestTranscriptSessionID_AllMalformedLines(t *testing.T) {
	jsonl := "not json at all\n{{{broken\nalso not json\n"
	scan := LatestTranscriptSessionID(strings.NewReader(jsonl))
	if scan.Lines != 3 {
		t.Fatalf("got Lines=%d, want 3 (every non-blank line counted even if malformed)", scan.Lines)
	}
	if scan.Parsed != 0 {
		t.Fatalf("got Parsed=%d, want 0 -- no line here is valid JSON", scan.Parsed)
	}
	if scan.Found() {
		t.Fatalf("got %+v, expected Found()=false when every line is malformed", scan)
	}
}

func TestLatestTranscriptSessionID_WellFormedButNoSessionIDField(t *testing.T) {
	jsonl := `{"type":"tool_result"}
{"message":{"role":"user"}}
`
	scan := LatestTranscriptSessionID(strings.NewReader(jsonl))
	if scan.Parsed != 2 {
		t.Fatalf("got Parsed=%d, want 2 -- both lines are valid JSON", scan.Parsed)
	}
	if scan.Found() {
		t.Fatalf("got %+v, expected Found()=false when no line names a sessionId", scan)
	}
}

func TestLatestTranscriptSessionID_MalformedLinesSkippedAmongValidOnes(t *testing.T) {
	jsonl := `{"sessionId":"` + testUUIDOurs + `"}
not json at all
{{{broken
`
	scan := LatestTranscriptSessionID(strings.NewReader(jsonl))
	if !scan.Found() || scan.SessionID != testUUIDOurs {
		t.Fatalf("got %+v, want SessionID=%q -- malformed trailing lines must not clobber the last valid reading", scan, testUUIDOurs)
	}
	if scan.Lines != 3 || scan.Parsed != 1 {
		t.Fatalf("got %+v, want Lines=3 Parsed=1", scan)
	}
}

// ---- CheckSessionID ----

func TestCheckSessionID_MatchDoesNotAbort(t *testing.T) {
	scan := SessionIDScan{SessionID: testUUIDOurs, Lines: 1, Parsed: 1}
	r := CheckSessionID(testUUIDOurs, scan)
	if r.Abort {
		t.Fatalf("expected no abort on a matching session id, got %+v", r)
	}
	if !r.SessionIDChecked || !r.SessionIDMatch {
		t.Fatalf("expected SessionIDChecked=true SessionIDMatch=true, got %+v", r)
	}
}

func TestCheckSessionID_MismatchHardAborts(t *testing.T) {
	// The FB26 hard-fail contract: a resolved transcript naming a DIFFERENT session's id must
	// abort unconditionally -- there is no warn-only escape hatch, unlike the tier ceiling check.
	scan := SessionIDScan{SessionID: testUUIDOther, Lines: 1, Parsed: 1}
	r := CheckSessionID(testUUIDOurs, scan)
	if !r.Abort {
		t.Fatalf("expected hard abort on a session-id mismatch, got %+v", r)
	}
	if !r.SessionIDChecked || r.SessionIDMatch {
		t.Fatalf("expected SessionIDChecked=true SessionIDMatch=false, got %+v", r)
	}
	if !strings.Contains(r.Reason, testUUIDOther) || !strings.Contains(r.Reason, testUUIDOurs) {
		t.Fatalf("expected Reason to name both the transcript's and the caller's session id, got %q", r.Reason)
	}
}
