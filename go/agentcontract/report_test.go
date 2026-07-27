package agentcontract

import (
	"strings"
	"testing"
)

// TestReport_ReviewerCheckedLimitAlwaysStated checks that the completeness-not-quality limit is present on both a clean and a failing report.
func TestReport_ReviewerCheckedLimitAlwaysStated(t *testing.T) {
	clean := NewReport(1, 1, nil)
	if !clean.Pass() {
		t.Fatalf("expected a report with no findings to pass")
	}
	if len(clean.ReviewerChecked) == 0 {
		t.Fatalf("expected the completeness-not-quality limit to be stated even on a clean pass")
	}

	var buf strings.Builder
	if err := clean.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "reviewer-checked") && !strings.Contains(buf.String(), "Reviewer-checked") {
		t.Fatalf("expected the rendered report to state the reviewer-checked limit, got:\n%s", buf.String())
	}

	dirty := NewReport(1, 1, []Finding{{Rule: "MATRIX-MISSING-CELL", Brief: "a.md", Message: "x"}})
	if dirty.Pass() {
		t.Fatalf("expected a report with findings to fail")
	}
	if len(dirty.ReviewerChecked) == 0 {
		t.Fatalf("expected the completeness-not-quality limit to be stated on a failing report too")
	}
}

// TestReport_DeterministicOrdering checks that two reports built from the same findings render findings in the same, sorted order.
func TestReport_DeterministicOrdering(t *testing.T) {
	findings := []Finding{
		{Rule: "Z-RULE", Brief: "b.md", Message: "x"},
		{Rule: "A-RULE", Brief: "a.md", Message: "y"},
		{Rule: "B-RULE", Brief: "a.md", Message: "z"},
	}
	r1 := NewReport(1, 2, findings)
	r2 := NewReport(1, 2, findings)
	for i := range r1.Findings {
		if r1.Findings[i] != r2.Findings[i] {
			t.Fatalf("expected identical input to produce identical ordering")
		}
	}
	if r1.Findings[0].Brief != "a.md" || r1.Findings[0].Rule != "A-RULE" {
		t.Fatalf("expected findings sorted by brief then rule, got: %+v", r1.Findings)
	}
}

// TestReport_DeterministicWithinSameBriefAndRule checks that several findings sharing one
// (Brief, Rule) — e.g. one brief missing a cell against each of two siblings — render in a
// stable order, since they originate from Go map iteration.
func TestReport_DeterministicWithinSameBriefAndRule(t *testing.T) {
	in := []Finding{
		{Rule: "MATRIX-MISSING-CELL", Brief: "a.md", Message: "against sibling \"c\""},
		{Rule: "MATRIX-MISSING-CELL", Brief: "a.md", Message: "against sibling \"b\""},
	}
	want := NewReport(1, 3, in).Findings
	for i := 0; i < 20; i++ {
		got := NewReport(1, 3, in).Findings
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("nondeterministic ordering for equal (Brief, Rule): %+v vs %+v", got, want)
			}
		}
	}
	if want[0].Message >= want[1].Message {
		t.Fatalf("expected message to break ties in ascending order, got: %+v", want)
	}
}
