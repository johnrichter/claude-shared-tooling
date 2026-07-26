package agentcontract

import "testing"

func brief(name string, discriminators map[string]Discriminator) *Brief {
	return &Brief{
		Path: name + ".md",
		Frontmatter: Frontmatter{
			Name: name,
			Contract: Contract{
				Discriminators: discriminators,
			},
		},
	}
}

func hasRule(findings []Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

// TestCheckMatrix_RosterOfOnePassesWithEmptySiblingSet checks that a roster of one member passes with no declared discriminator cells.
func TestCheckMatrix_RosterOfOnePassesWithEmptySiblingSet(t *testing.T) {
	solo := brief("solo", nil)
	r := Roster{Dir: "agents", Briefs: []*Brief{solo}}

	findings := CheckMatrix(r)
	if len(findings) != 0 {
		t.Fatalf("roster of one with no declared discriminators must pass, got: %v", findings)
	}
}

// TestCheckMatrix_RosterOfMultipleWithEmptySiblingSetFails checks that a roster of more than one member fails when a member declares no discriminator cells.
func TestCheckMatrix_RosterOfMultipleWithEmptySiblingSetFails(t *testing.T) {
	a := brief("a", nil)
	b := brief("b", nil)
	r := Roster{Dir: "agents", Briefs: []*Brief{a, b}}

	findings := CheckMatrix(r)
	if !hasRule(findings, "MATRIX-MISSING-CELL") {
		t.Fatalf("expected MATRIX-MISSING-CELL for both members of a 2-agent roster declaring no cells, got: %v", findings)
	}
}

// TestCheckMatrix_MissingOrderedPairCellFails checks that omitting the cell for one sibling out of several is reported against the omitting brief.
func TestCheckMatrix_MissingOrderedPairCellFails(t *testing.T) {
	a := brief("a", map[string]Discriminator{
		"b": {Relation: RelationDiscriminator, Reason: "a owns X, b owns Y"},
		// "c" cell intentionally omitted.
	})
	b := brief("b", map[string]Discriminator{
		"a": {Relation: RelationDiscriminator, Reason: "b owns Y, a owns X"},
		"c": {Relation: RelationNotConfusable, Reason: "different domain"},
	})
	c := brief("c", map[string]Discriminator{
		"a": {Relation: RelationNotConfusable, Reason: "different domain"},
		"b": {Relation: RelationNotConfusable, Reason: "different domain"},
	})
	r := Roster{Dir: "agents", Briefs: []*Brief{a, b, c}}

	findings := CheckMatrix(r)
	found := false
	for _, f := range findings {
		if f.Rule == "MATRIX-MISSING-CELL" && f.Brief == a.Path {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a's missing cell against c to be reported, got: %v", findings)
	}
}

// TestCheckMatrix_AuthorNominatedSubsetDoesNotSatisfyCompleteness checks that declaring a cell against a name outside the roster neither satisfies the real sibling's cell nor passes silently.
func TestCheckMatrix_AuthorNominatedSubsetDoesNotSatisfyCompleteness(t *testing.T) {
	// a declares a cell against a name that is not even in the roster (a nominated, not
	// derived, sibling set) instead of against its real sibling b.
	a := brief("a", map[string]Discriminator{
		"not-a-real-sibling": {Relation: RelationDiscriminator, Reason: "irrelevant"},
	})
	b := brief("b", map[string]Discriminator{
		"a": {Relation: RelationDiscriminator, Reason: "b owns Y, a owns X"},
	})
	r := Roster{Dir: "agents", Briefs: []*Brief{a, b}}

	findings := CheckMatrix(r)
	if !hasRule(findings, "MATRIX-STALE-SIBLING") {
		t.Errorf("expected the nominated non-member name to be flagged stale, got: %v", findings)
	}
	if !hasRule(findings, "MATRIX-MISSING-CELL") {
		t.Errorf("expected the real sibling b's cell to still be reported missing on a, got: %v", findings)
	}
}

// TestCheckMatrix_FuzzyBoundaryWithoutTieBreakFails checks that a cell marked fuzzy with no tie_break fails, independent of any other cell in the same roster.
func TestCheckMatrix_FuzzyBoundaryWithoutTieBreakFails(t *testing.T) {
	a := brief("a", map[string]Discriminator{
		"b": {Relation: RelationDiscriminator, Reason: "mostly separate, but overlap on X", Fuzzy: true},
	})
	b := brief("b", map[string]Discriminator{
		"a": {Relation: RelationDiscriminator, Reason: "mostly separate, but overlap on X", Fuzzy: true, TieBreak: "whichever agent is already mid-task wins"},
	})
	r := Roster{Dir: "agents", Briefs: []*Brief{a, b}}

	findings := CheckMatrix(r)
	if !hasRule(findings, "MATRIX-MISSING-TIEBREAK") {
		t.Fatalf("expected a fuzzy cell with no tie_break to fail, got: %v", findings)
	}
	for _, f := range findings {
		if f.Brief == b.Path && f.Rule == "MATRIX-MISSING-TIEBREAK" {
			t.Fatalf("b declared a tie_break and should not be flagged, got: %v", findings)
		}
	}
}

// TestCheckMatrix_SelfReferenceFlagged checks that a brief declaring a cell against its own name is flagged.
func TestCheckMatrix_SelfReferenceFlagged(t *testing.T) {
	a := brief("a", map[string]Discriminator{
		"a": {Relation: RelationNotConfusable, Reason: "n/a"},
	})
	r := Roster{Dir: "agents", Briefs: []*Brief{a}}

	findings := CheckMatrix(r)
	if !hasRule(findings, "MATRIX-SELF-REFERENCE") {
		t.Fatalf("expected a self-referencing cell to be flagged, got: %v", findings)
	}
}

// TestCheckMatrix_BadRelationAndMissingReasonFlagged checks that an unrecognized relation value and an empty reason are both flagged on the same cell.
func TestCheckMatrix_BadRelationAndMissingReasonFlagged(t *testing.T) {
	a := brief("a", map[string]Discriminator{
		"b": {Relation: "sort-of-different", Reason: ""},
	})
	b := brief("b", map[string]Discriminator{
		"a": {Relation: RelationNotConfusable, Reason: "different domain"},
	})
	r := Roster{Dir: "agents", Briefs: []*Brief{a, b}}

	findings := CheckMatrix(r)
	if !hasRule(findings, "MATRIX-BAD-RELATION") {
		t.Errorf("expected an unrecognized relation to be flagged, got: %v", findings)
	}
	if !hasRule(findings, "MATRIX-MISSING-REASON") {
		t.Errorf("expected an empty reason to be flagged, got: %v", findings)
	}
}
