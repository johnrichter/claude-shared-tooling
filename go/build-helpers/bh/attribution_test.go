package bh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin MEASURED per-task cost attribution (M2.P1.T4) against the golden fixtures. They
// assert: (1) the shared-prefix pair M2.P1.T3 / M2.P1.T31 attribute to their OWN cost with NO cross-
// contamination (the exact-match guard's regression pin, requested by the T3 reviewer); (2) an
// unmappable transcript lands in the flagged even-split pool, never silently mis-attributed or
// dropped; (3) the concurrent-fan-out scenario (agenta3→M2.P1.T2, agenta4→M2.P1.T3) attributes each
// task's measured cost as the exact sum of its transcript(s), matching EXPECTED.md; (4) a task with
// multiple transcripts (agenta1+agenta2→M2.P1.T1, two models/two roles) sums across them. Fixtures:
// testdata/attribution/ (shared-prefix + unmappable) and the existing testdata/accounting/subagents/
// (fan-out + multi-transcript), so the accounting grand-total fixtures are reused, never disturbed.

const attributionDir = "testdata/attribution"

// attribSourcesFrom opens the given fixture paths as AttribSources (whole-file readers) and returns
// the handles to close.
func attribSourcesFrom(t *testing.T, paths ...string) ([]AttribSource, []*os.File) {
	t.Helper()
	var sources []AttribSource
	var handles []*os.File
	for _, p := range paths {
		fh, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		handles = append(handles, fh)
		sources = append(sources, AttribSource{FileID: p, Reader: fh})
	}
	return sources, handles
}

// TestAttribute_SharedPrefixNoCrossContamination is the exact-match guard's regression pin: two
// transcripts in the same batch whose first-turn task IDs share a prefix (M2.P1.T3 and M2.P1.T31)
// must each receive ONLY their own cost. A substring/prefix match (the bug this guards) would let
// M2.P1.T31's transcript leak into M2.P1.T3 (or vice versa); exact full-string equality forbids it.
// Each fixture's first user turn also NAMES the other task in its summary, proving extraction takes
// the LEADING `Task <id>` token, not a later mention, and that the greedy digit run keeps T31 whole.
func TestAttribute_SharedPrefixNoCrossContamination(t *testing.T) {
	rates := loadTestRates(t)
	b3 := filepath.Join(attributionDir, "subagents", "agent-b3.jsonl")
	b31 := filepath.Join(attributionDir, "subagents", "agent-b31.jsonl")
	sources, handles := attribSourcesFrom(t, b3, b31)
	defer closeHandles(handles)

	attr, err := Attribute(sources, []string{"M2.P1.T3", "M2.P1.T31"}, rates, "t")
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	if attr.EvenSplit != nil {
		t.Fatalf("no transcript should be unmappable; got even-split pool %+v", attr.EvenSplit)
	}
	if len(attr.Unmappable) != 0 {
		t.Fatalf("unexpected unmappable transcripts: %+v", attr.Unmappable)
	}

	t3 := attr.Tasks["M2.P1.T3"]
	t31 := attr.Tasks["M2.P1.T31"]
	if t3 == nil || t31 == nil {
		t.Fatalf("both tasks must be attributed; got T3=%v T31=%v", t3, t31)
	}
	// Each task carries exactly its own single transcript — no leakage.
	if len(t3.Transcripts) != 1 || !strings.HasSuffix(t3.Transcripts[0], "agent-b3.jsonl") {
		t.Fatalf("M2.P1.T3 transcripts = %v, want only agent-b3.jsonl", t3.Transcripts)
	}
	if len(t31.Transcripts) != 1 || !strings.HasSuffix(t31.Transcripts[0], "agent-b31.jsonl") {
		t.Fatalf("M2.P1.T31 transcripts = %v, want only agent-b31.jsonl", t31.Transcripts)
	}
	// Distinct measured costs, each its own (EXPECTED.md attribution fixtures).
	assertBuckets(t, t3.Models["claude-sonnet-5"], wantBuckets(2000, 400, 200, 100, 200, 1), "T3 sonnet-5")
	assertBuckets(t, t31.Models["claude-opus-4-8"], wantBuckets(1000, 200, 100, 200, 100, 1), "T31 opus-4-8")
	assertClose(t, t3.CostUSD, 0.01173, "T3 measured cost")
	assertClose(t, t31.CostUSD, 0.00985, "T31 measured cost")
	if t3.CostAttribution != "measured" || t31.CostAttribution != "measured" {
		t.Fatalf("mapped tasks must be flagged measured; got T3=%q T31=%q", t3.CostAttribution, t31.CostAttribution)
	}
	// T3 must NOT carry T31's opus buckets and vice versa — the anti-contamination assertion.
	if _, leaked := t3.Models["claude-opus-4-8"]; leaked {
		t.Fatal("M2.P1.T3 leaked opus-4-8 buckets from M2.P1.T31 — exact-match guard failed")
	}
	if _, leaked := t31.Models["claude-sonnet-5"]; leaked {
		t.Fatal("M2.P1.T31 leaked sonnet-5 buckets from M2.P1.T3 — exact-match guard failed")
	}
}

// TestExtractTaskID_GreedyAndLeftmost unit-pins the two anchoring properties of the extractor without
// I/O: greedy digit capture keeps T31 whole (never truncates to T3), and the leftmost match is the
// leading dispatch token even when the summary embeds another task ID after it.
func TestExtractTaskID_GreedyAndLeftmost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Task M2.P1.T31 — supersedes M2.P1.T3.", "M2.P1.T31"}, // greedy: full T31, leftmost: the leading token
		{"Task M2.P1.T3 — do NOT touch M2.P1.T31.", "M2.P1.T3"},
		{"Task M10.P2.T100 — big numbers.", "M10.P2.T100"},
		{"no identifier here at all", ""},
	}
	for _, c := range cases {
		if got := extractTaskID(c.in); got != c.want {
			t.Errorf("extractTaskID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAttribute_UnmappableGoesToFlaggedEvenSplit proves an unmappable transcript (first user turn
// carries no task ID) is NOT dropped and NOT mis-attributed: its full cost lands in the flagged
// even-split pool, distributed across the batch's known tasks, and it is surfaced in Unmappable.
func TestAttribute_UnmappableGoesToFlaggedEvenSplit(t *testing.T) {
	rates := loadTestRates(t)
	b3 := filepath.Join(attributionDir, "subagents", "agent-b3.jsonl")
	b31 := filepath.Join(attributionDir, "subagents", "agent-b31.jsonl")
	bx := filepath.Join(attributionDir, "subagents", "agent-bx.jsonl")
	sources, handles := attribSourcesFrom(t, b3, b31, bx)
	defer closeHandles(handles)

	known := []string{"M2.P1.T3", "M2.P1.T31"}
	attr, err := Attribute(sources, known, rates, "t")
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	// Mapped tasks keep their exact measured cost — unaffected by the unmappable transcript.
	assertClose(t, attr.Tasks["M2.P1.T3"].CostUSD, 0.01173, "T3 measured cost (unchanged by unmappable)")
	assertClose(t, attr.Tasks["M2.P1.T31"].CostUSD, 0.00985, "T31 measured cost (unchanged by unmappable)")

	// bx is surfaced as unmappable, with the no-task-id reason.
	if len(attr.Unmappable) != 1 || !strings.HasSuffix(attr.Unmappable[0].Transcript, "agent-bx.jsonl") {
		t.Fatalf("Unmappable = %+v, want exactly agent-bx.jsonl", attr.Unmappable)
	}
	if attr.Unmappable[0].ExtractedID != "" || attr.Unmappable[0].Reason != "no task-id in first user turn" {
		t.Fatalf("unmappable detail = %+v, want empty id + no-task-id reason", attr.Unmappable[0])
	}
	// Its cost is in the flagged pool, split across the two known tasks — never dropped.
	if attr.EvenSplit == nil {
		t.Fatal("even-split pool must be present for the unmappable transcript")
	}
	assertClose(t, attr.EvenSplit.CostUSD, 0.003375, "even-split pool cost = bx full cost")
	assertClose(t, attr.EvenSplit.PerTaskCostUSD, 0.003375/2, "per-task even-split share")
	if attr.EvenSplit.CostAttribution != "batch-even-split" {
		t.Fatalf("pool must be flagged batch-even-split, got %q", attr.EvenSplit.CostAttribution)
	}
	if strings.Join(attr.EvenSplit.Tasks, ",") != "M2.P1.T3,M2.P1.T31" {
		t.Fatalf("pool split across %v, want [M2.P1.T3 M2.P1.T31]", attr.EvenSplit.Tasks)
	}
	// Conservation: measured + pooled == the sum of all three transcripts' true cost (nothing lost).
	total := attr.Tasks["M2.P1.T3"].CostUSD + attr.Tasks["M2.P1.T31"].CostUSD + attr.EvenSplit.CostUSD
	assertClose(t, total, 0.01173+0.00985+0.003375, "measured + pooled == whole-batch true cost")
}

// TestAttribute_ExtractedIDNotInKnownSetIsUnmappable proves the OTHER unmappable path: a transcript
// whose first turn DOES carry a well-formed task ID, but one absent from the known set, is not
// force-matched to a look-alike — it is surfaced as unmappable with the extracted ID and pooled.
func TestAttribute_ExtractedIDNotInKnownSetIsUnmappable(t *testing.T) {
	rates := loadTestRates(t)
	b3 := filepath.Join(attributionDir, "subagents", "agent-b3.jsonl")   // M2.P1.T3
	b31 := filepath.Join(attributionDir, "subagents", "agent-b31.jsonl") // M2.P1.T31
	sources, handles := attribSourcesFrom(t, b3, b31)
	defer closeHandles(handles)

	// Known set has ONLY the shorter T3 — the belt-and-suspenders check that T31 does NOT collide
	// with the T3 entry by prefix and is instead correctly reported unmappable.
	attr, err := Attribute(sources, []string{"M2.P1.T3"}, rates, "t")
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	if attr.Tasks["M2.P1.T3"] == nil {
		t.Fatal("M2.P1.T3 must still map (exact match present in known set)")
	}
	if _, wrong := attr.Tasks["M2.P1.T31"]; wrong {
		t.Fatal("M2.P1.T31 must NOT map to any task when absent from known set")
	}
	if len(attr.Unmappable) != 1 || attr.Unmappable[0].ExtractedID != "M2.P1.T31" {
		t.Fatalf("Unmappable = %+v, want the extracted M2.P1.T31 surfaced", attr.Unmappable)
	}
	if attr.Unmappable[0].Reason != "extracted task-id not in known set" {
		t.Fatalf("reason = %q, want extracted-id-not-in-known-set", attr.Unmappable[0].Reason)
	}
	assertClose(t, attr.EvenSplit.CostUSD, 0.00985, "T31 cost pooled, not leaked into T3")
	assertClose(t, attr.Tasks["M2.P1.T3"].CostUSD, 0.01173, "T3 unaffected by pooled T31")
}

// TestAttribute_BareStringContentShape is the adversarial case flagged by the engineer: a real subagent
// transcript's first user turn `message.content` may be a BARE STRING, not the array-of-`{type,text}`-
// blocks shape every other fixture in this suite uses. If contentText only handled the array shape,
// extraction would silently fail (empty text -> "" -> unmappable/pooled), masking a real transcript's
// task ID. Fixture: agent-bstr.jsonl, content is a plain JSON string carrying "Task M2.P1.T3 ...".
func TestAttribute_BareStringContentShape(t *testing.T) {
	rates := loadTestRates(t)
	bstr := filepath.Join(attributionDir, "subagents", "agent-bstr.jsonl")
	sources, handles := attribSourcesFrom(t, bstr)
	defer closeHandles(handles)

	attr, err := Attribute(sources, []string{"M2.P1.T3", "M2.P1.T31"}, rates, "t")
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	if len(attr.Unmappable) != 0 {
		t.Fatalf("bare-string content must still yield a task-id match; got unmappable=%+v", attr.Unmappable)
	}
	t3 := attr.Tasks["M2.P1.T3"]
	if t3 == nil {
		t.Fatal("bare-string first-user-turn content did not extract M2.P1.T3 — contentText does not handle the bare-string shape")
	}
	if len(t3.Transcripts) != 1 || !strings.HasSuffix(t3.Transcripts[0], "agent-bstr.jsonl") {
		t.Fatalf("M2.P1.T3 transcripts = %v, want only agent-bstr.jsonl", t3.Transcripts)
	}
	assertBuckets(t, t3.Models["claude-sonnet-5"], wantBuckets(500, 100, 0, 50, 50, 1), "bstr sonnet-5")
}

// TestAttribute_ConcurrentFanOutAndMultiTranscript reuses the accounting fan-out fixtures to pin the
// two remaining acceptance points against EXPECTED.md: (a) concurrent fan-out agenta3→M2.P1.T2 /
// agenta4→M2.P1.T3 attributes each task's measured cost as the exact sum of its transcript; (b) a
// task with multiple transcripts (agenta1+agenta2→M2.P1.T1, two models/two roles) sums across them.
func TestAttribute_ConcurrentFanOutAndMultiTranscript(t *testing.T) {
	rates := loadTestRates(t)
	sub := filepath.Join(accountingDir, "subagents")
	a1 := filepath.Join(sub, "agent-agenta1.jsonl") // M2.P1.T1 sonnet-5 (software-engineer)
	a2 := filepath.Join(sub, "agent-agenta2.jsonl") // M2.P1.T1 opus-4-8 (quality-reviewer)
	a3 := filepath.Join(sub, "agent-agenta3.jsonl") // M2.P1.T2 sonnet-5 (test-engineer)
	a4 := filepath.Join(sub, "agent-agenta4.jsonl") // M2.P1.T3 opus-4-8 (software-engineer)
	sources, handles := attribSourcesFrom(t, a1, a2, a3, a4)
	defer closeHandles(handles)

	attr, err := Attribute(sources, []string{"M2.P1.T1", "M2.P1.T2", "M2.P1.T3"}, rates, "t")
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	if attr.EvenSplit != nil || len(attr.Unmappable) != 0 {
		t.Fatalf("all four fan-out transcripts must map; unmappable=%+v pool=%+v", attr.Unmappable, attr.EvenSplit)
	}

	// Concurrent fan-out: each of T2/T3 gets exactly its own transcript's measured cost.
	assertClose(t, attr.Tasks["M2.P1.T2"].CostUSD, 0.02349, "M2.P1.T2 (agenta3) measured cost")
	assertClose(t, attr.Tasks["M2.P1.T3"].CostUSD, 0.02845, "M2.P1.T3 (agenta4) measured cost")

	// Multi-transcript task: M2.P1.T1 = agenta1 (sonnet-5) + agenta2 (opus-4-8), summed across both.
	t1 := attr.Tasks["M2.P1.T1"]
	if len(t1.Transcripts) != 2 {
		t.Fatalf("M2.P1.T1 transcripts = %v, want 2 (agenta1 + agenta2)", t1.Transcripts)
	}
	assertBuckets(t, t1.Models["claude-sonnet-5"], wantBuckets(4000, 1000, 500, 500, 500, 2), "T1 sonnet-5 (agenta1)")
	assertBuckets(t, t1.Models["claude-opus-4-8"], wantBuckets(2000, 500, 0, 1000, 400, 1), "T1 opus-4-8 (agenta2)")
	assertClose(t, t1.CostUSD, 0.050025, "M2.P1.T1 combined measured cost")
}

// TestAttribute_NestedWorkflowDepth is ACC2's (M13.P2.T2) assertion 3 (testdata/nested/EXPECTED.md):
// per-task attribution at the REAL nested-workflow depth (session/subagents/workflows/wf_*/), not
// just the flat sibling layout the other tests in this file use. It also pins AC1 directly: the
// SAME DiscoverSubagentTranscripts() call this test uses to build attribution's AttribSource list is
// the identical seam accounting_test.go's TestAccounting_NestedOIsolationAndGrandTotal uses to build
// its TranscriptSource list — byte-identical FileID strings, not two independently re-derived sets.
// Pre-change, neither depth-3 nested agent is discovered at all, so M13.P2.T3/M13.P2.T31 would
// receive zero measured cost and degrade to the even-split pool this test proves absent.
func TestAttribute_NestedWorkflowDepth(t *testing.T) {
	rates := loadTestRates(t)
	mainPath := filepath.Join(nestedDir, "orchestrator.jsonl")

	subs, err := DiscoverSubagentTranscripts(mainPath)
	if err != nil {
		t.Fatalf("DiscoverSubagentTranscripts: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("discovered %d subagent transcripts, want 3; got %v", len(subs), subs)
	}
	sources, handles := attribSourcesFrom(t, subs...)
	defer closeHandles(handles)

	known := []string{"M13.P2.T1", "M13.P2.T3", "M13.P2.T31"}
	attr, err := Attribute(sources, known, rates, "t")
	if err != nil {
		t.Fatalf("Attribute: %v", err)
	}
	if attr.EvenSplit != nil || len(attr.Unmappable) != 0 {
		t.Fatalf("all 3 discovered subagents must map cleanly at real nesting depth; unmappable=%+v pool=%+v", attr.Unmappable, attr.EvenSplit)
	}

	t1 := attr.Tasks["M13.P2.T1"]
	t3 := attr.Tasks["M13.P2.T3"]
	t31 := attr.Tasks["M13.P2.T31"]
	if t1 == nil || t3 == nil || t31 == nil {
		t.Fatalf("all 3 known tasks must be attributed; got T1=%v T3=%v T31=%v", t1, t3, t31)
	}

	if len(t1.Transcripts) != 1 || !strings.HasSuffix(t1.Transcripts[0], "agent-direct.jsonl") {
		t.Fatalf("M13.P2.T1 transcripts = %v, want only agent-direct.jsonl", t1.Transcripts)
	}
	if len(t3.Transcripts) != 1 || !strings.HasSuffix(t3.Transcripts[0], "agent-e1.jsonl") {
		t.Fatalf("M13.P2.T3 transcripts = %v, want only the DEPTH-3 agent-e1.jsonl", t3.Transcripts)
	}
	if len(t31.Transcripts) != 1 || !strings.HasSuffix(t31.Transcripts[0], "agent-e2.jsonl") {
		t.Fatalf("M13.P2.T31 transcripts = %v, want only the DEPTH-3 agent-e2.jsonl", t31.Transcripts)
	}

	assertClose(t, t1.CostUSD, 0.0343, "M13.P2.T1 (agent-direct) measured cost")
	assertClose(t, t3.CostUSD, 0.015435, "M13.P2.T3 (agent-e1, depth-3) measured cost")
	assertClose(t, t31.CostUSD, 0.01715, "M13.P2.T31 (agent-e2, depth-3) measured cost")

	// Shared-prefix anti-contamination guard, reproduced at real nesting depth: T3 must never carry
	// T31's opus-4-8 buckets and vice versa.
	if _, leaked := t3.Models["claude-opus-4-8"]; leaked {
		t.Fatal("M13.P2.T3 leaked opus-4-8 buckets from M13.P2.T31 at nested depth — exact-match guard failed")
	}
	if _, leaked := t31.Models["claude-sonnet-5"]; leaked {
		t.Fatal("M13.P2.T31 leaked sonnet-5 buckets from M13.P2.T3 at nested depth — exact-match guard failed")
	}
}
