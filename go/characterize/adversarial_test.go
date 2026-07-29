package characterize

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// TestAdversarialUnrecognizedSurfaceType_NeverFabricated checks a candidate naming a surface type
// outside the manifest schema's closed enum is redirected to a gap, never coerced into a
// SurfaceOther or otherwise let through as a fabricated surface.
func TestAdversarialUnrecognizedSurfaceType_NeverFabricated(t *testing.T) {
	ids := newIDMinter("plugin")
	cs := candidateSurface{
		Type:     "not-a-real-surface-kind",
		Trigger:  "some trigger long enough to pass the minimum length check",
		Citation: Citation{Path: "fixture-repo/fixture-plugin/commands/hello.md"},
	}
	_, _, reason, ok := resolveSurface(ids, "fixture-repo/fixture-plugin", fixturePluginDir, cs)
	if ok {
		t.Fatal("resolveSurface accepted an unrecognized surface type")
	}
	if reason == "" {
		t.Error("reason is empty, want an explanation naming the unrecognized type")
	}
}

// TestAdversarialShortTrigger_RejectedNotFabricated checks a trigger below the schema's own
// minimum length is rejected rather than padded, truncated, or passed through.
func TestAdversarialShortTrigger_RejectedNotFabricated(t *testing.T) {
	ids := newIDMinter("plugin")
	cs := candidateSurface{
		Type:     SurfaceCommand,
		Trigger:  "short",
		Citation: Citation{Path: "fixture-repo/fixture-plugin/commands/hello.md"},
	}
	_, _, reason, ok := resolveSurface(ids, "fixture-repo/fixture-plugin", fixturePluginDir, cs)
	if ok {
		t.Fatal("resolveSurface accepted a trigger below the minimum length")
	}
	if reason == "" {
		t.Error("reason is empty, want an explanation naming the length violation")
	}
}

// TestAdversarialWeakSpotOutsidePluginPath_FoldedIntoGapNotSurface checks a weak spot whose
// citation names a path outside the plugin's own repo prefix is dropped from the surface's own
// weak spots and folded into a gap that still identifies the surface it was claimed against --
// never silently discarded, never let through as an unverified weak spot.
func TestAdversarialWeakSpotOutsidePluginPath_FoldedIntoGapNotSurface(t *testing.T) {
	ids := newIDMinter("plugin")
	cs := candidateSurface{
		Type:     SurfaceCommand,
		Name:     "hello",
		Trigger:  "Invoked via the /hello slash command per its own frontmatter.",
		Citation: Citation{Path: "fixture-repo/fixture-plugin/commands/hello.md"},
		WeakSpots: []candidateWeakSpot{
			{
				Description: "a claimed weak spot with a citation pointing outside the plugin entirely",
				Basis:       "this basis text is long enough to pass the minimum length check",
				Citation:    Citation{Path: "some-other-repo/other-plugin/file.md"},
			},
		},
	}
	surface, gaps, _, ok := resolveSurface(ids, "fixture-repo/fixture-plugin", fixturePluginDir, cs)
	if !ok {
		t.Fatal("resolveSurface rejected the surface itself over an unrelated weak-spot citation defect")
	}
	if len(surface.WeakSpots) != 0 {
		t.Fatalf("surface.WeakSpots = %+v, want the out-of-plugin weak spot dropped, not fabricated", surface.WeakSpots)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want exactly one gap for the dropped weak spot", gaps)
	}
	if gaps[0].Area != "weak spot claimed on candidate surface: hello" {
		t.Errorf("gap.Area = %q, want it to name the surface the weak spot was claimed against", gaps[0].Area)
	}
}

// TestAdversarialUndersizedCouldNotDetermine_DroppedAndSummarized checks a could_not_determine
// entry whose area/reason falls below the manifest schema's own minimum lengths is never emitted
// underfilled -- it is dropped, and the drop itself is reported as one summary gap rather than
// silently vanishing.
func TestAdversarialUndersizedCouldNotDetermine_DroppedAndSummarized(t *testing.T) {
	ids := newIDMinter("plugin")
	cand := candidateManifest{
		CouldNotDetermine: []candidateGap{
			{Area: "x", Reason: "too short"},
			{Area: "a fine area name", Reason: "a perfectly adequate reason text here"},
		},
	}
	_, gaps := buildManifest(ids, "fixture-repo/fixture-plugin", fixturePluginDir, cand)
	if len(gaps) != 2 {
		t.Fatalf("gaps = %+v, want 2 (the valid entry plus one summary gap for the dropped one)", gaps)
	}
	var sawSummary, sawValid bool
	for _, g := range gaps {
		if g.Area == "characterizing reply quality" {
			sawSummary = true
		}
		if g.Area == "a fine area name" {
			sawValid = true
		}
	}
	if !sawSummary {
		t.Error("no summary gap reported for the dropped undersized could_not_determine entry")
	}
	if !sawValid {
		t.Error("the valid could_not_determine entry did not survive buildManifest")
	}
}

// TestAdversarialCitation_DirectoryPathRejected checks a citation naming a directory (not a file)
// fails resolution -- a directory proves nothing about a specific surface's own contents.
func TestAdversarialCitation_DirectoryPathRejected(t *testing.T) {
	err := resolveCitation("fixture-repo/fixture-plugin", fixturePluginDir, Citation{Path: "fixture-repo/fixture-plugin/commands"})
	if err == nil {
		t.Fatal("resolveCitation accepted a directory path as a valid citation")
	}
}

// TestAdversarialCitation_LineRangePastEndOfFileRejected checks a citation whose line range runs
// past the cited file's actual line count fails resolution -- catching a claim that cites text the
// file does not contain.
func TestAdversarialCitation_LineRangePastEndOfFileRejected(t *testing.T) {
	err := resolveCitation("fixture-repo/fixture-plugin", fixturePluginDir, Citation{
		Path:  "fixture-repo/fixture-plugin/commands/hello.md",
		Lines: []int{1, 9999},
	})
	if err == nil {
		t.Fatal("resolveCitation accepted a line range past the file's actual length")
	}
}

// TestAdversarialCitation_OutsidePluginPathRejected checks a citation naming a path outside the
// plugin's own repo-qualified prefix is rejected -- characterization only reads the one plugin it
// was asked to, never cites another plugin's or repo's files as evidence.
func TestAdversarialCitation_OutsidePluginPathRejected(t *testing.T) {
	err := resolveCitation("fixture-repo/fixture-plugin", fixturePluginDir, Citation{Path: "some-other-repo/other-plugin/file.md"})
	if err == nil {
		t.Fatal("resolveCitation accepted a citation path outside the characterized plugin")
	}
}

// TestAdversarialCitation_InvertedLineRangeRejected checks a citation whose line range is
// inverted (end before start) is rejected rather than silently accepted or swapped.
func TestAdversarialCitation_InvertedLineRangeRejected(t *testing.T) {
	err := resolveCitation("fixture-repo/fixture-plugin", fixturePluginDir, Citation{
		Path:  "fixture-repo/fixture-plugin/commands/hello.md",
		Lines: []int{5, 2},
	})
	if err == nil {
		t.Fatal("resolveCitation accepted an inverted line range")
	}
}

// TestAdversarialParseCandidate_MalformedJSONRejected checks a probe reply that is not valid JSON
// at all is reported as a parse error rather than silently treated as an empty manifest.
func TestAdversarialParseCandidate_MalformedJSONRejected(t *testing.T) {
	if _, err := parseCandidate("this is not json"); err == nil {
		t.Fatal("parseCandidate accepted a non-JSON reply")
	}
}

// TestAdversarialParseCandidate_StripsJSONCodeFence checks a probe reply wrapped in a
// ```json fenced block -- despite being told to reply with bare JSON -- still parses, since a
// model does not always follow that instruction to the letter.
func TestAdversarialParseCandidate_StripsJSONCodeFence(t *testing.T) {
	fenced := "```json\n{\"surfaces\": [], \"could_not_determine\": []}\n```"
	cand, err := parseCandidate(fenced)
	if err != nil {
		t.Fatalf("parseCandidate rejected a ```json-fenced reply: %v", err)
	}
	if cand.Surfaces == nil || len(cand.Surfaces) != 0 {
		t.Errorf("cand.Surfaces = %+v, want an empty (non-nil after decode) slice", cand.Surfaces)
	}
}

// TestAdversarialRun_ProbeReportsIsError checks a probe whose own result carries is_error=true
// fails the run outright -- never silently proceeds to build a manifest against a run the probe
// itself flagged as failed.
func TestAdversarialRun_ProbeReportsIsError(t *testing.T) {
	pr := probeResult{SessionID: "sess-err", TotalCostUSD: 0.01, IsError: true, Result: "{}"}
	stdout, err := json.Marshal(pr)
	if err != nil {
		t.Fatalf("marshal fixture probe result: %v", err)
	}
	run := func(_ context.Context, _ []string, _ sysops.Options) (*sysops.Result, error) {
		return &sysops.Result{ExitCode: 0, Stdout: stdout}, nil
	}
	opts := baseOptions(ProbeRunner(run))
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run succeeded despite the probe reporting is_error")
	}
}

// TestAdversarialAttemptedCitation_MalformedPathDropped checks a gap's attempted_citation is
// dropped (nil) rather than emitted, when the underlying candidate citation path is not
// schema-shaped -- an unresolvable AND malformed citation must never reach the manifest even as a
// best-effort attempted_citation, since that would itself fail the manifest schema.
func TestAdversarialAttemptedCitation_MalformedPathDropped(t *testing.T) {
	if got := attemptedCitation(Citation{Path: "has whitespace/in it"}); got != nil {
		t.Errorf("attemptedCitation = %+v, want nil for a malformed (whitespace-containing) path", got)
	}
	if got := attemptedCitation(Citation{}); got != nil {
		t.Errorf("attemptedCitation = %+v, want nil for the zero-value citation", got)
	}
	if got := attemptedCitation(Citation{Path: "fixture-repo/fixture-plugin/commands/hello.md"}); got == nil {
		t.Error("attemptedCitation = nil, want a non-nil pointer for a well-formed path")
	}
}
