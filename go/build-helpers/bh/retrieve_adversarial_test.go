package bh

import (
	"reflect"
	"testing"
)

// Test-engineer adversarial suite for M4.P2.T1 retrieval API, additive to the implementer's
// bh/retrieve_test.go. Targets: determinism across repeated calls, aliasing/mutation-safety of
// returned projections against the canonical struct, and boundary/garbage-input handling not
// covered by the implementer's suite.

func TestRetrievePlanOutlineDeterministicAcrossCalls(t *testing.T) {
	p := twoPhasePlan()
	a, err := RetrievePlan(p, RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	b, err := RetrievePlan(p, RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two outline calls on the same Plan diverged:\n%+v\n%+v", a, b)
	}
}

func TestRetrieveExecOutlineDeterministicAcrossCalls(t *testing.T) {
	ex := execFixture(t)
	a, err := RetrieveExec(ex, RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	b, err := RetrieveExec(ex, RetrieveInput{Level: LevelOutline})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two exec outline calls on the same ExecState diverged:\n%+v\n%+v", a, b)
	}
}

// A milestone/phase GroupView's Tasks[i].Deps is built by directly assigning t.Deps (a slice
// header), so it aliases the canonical Task's backing array. Mutating the returned projection
// must not corrupt the source Plan the caller may still hold — a real risk given "read-only" is
// an explicit acceptance criterion. This documents current behavior: retrieval does NOT deep-copy
// slice-typed fields, so a caller that mutates a returned Deps/Acceptance slice in place *will*
// corrupt the canonical Plan in the same process. Flagged as a finding, not silently accepted.
func TestRetrievePlanGroupDepsAliasesCanonicalSlice(t *testing.T) {
	p := validPlan()
	res, err := RetrievePlan(p, RetrieveInput{Level: LevelMilestone, ID: "M1"})
	if err != nil {
		t.Fatal(err)
	}
	gv := res.(GroupView)
	var t2 *OutlineEntry
	for i := range gv.Tasks {
		if gv.Tasks[i].ID == "M1.P1.T2" {
			t2 = &gv.Tasks[i]
		}
	}
	if t2 == nil || len(t2.Deps) == 0 {
		t.Fatal("fixture invariant broken: M1.P1.T2 should have deps")
	}
	original := t2.Deps[0]
	t2.Deps[0] = "CORRUPTED"
	// Re-fetch fresh from the same canonical Plan value.
	res2, err := RetrievePlan(p, RetrieveInput{Level: LevelTask, ID: "M1.P1.T2"})
	if err != nil {
		t.Fatal(err)
	}
	freshDeps := res2.(Task).Deps
	if freshDeps[0] == "CORRUPTED" {
		t.Fatalf("MUTATION LEAK: mutating a returned GroupView projection corrupted the canonical Plan's Task.Deps (in-process aliasing); want independent copy, got shared backing array (original was %q)", original)
	}
}

// Same aliasing risk for field-level retrieval of a slice-typed field (e.g. Acceptance).
func TestRetrievePlanFieldSliceAliasesCanonicalSlice(t *testing.T) {
	p := validPlan()
	res, err := RetrievePlan(p, RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	acc, ok := res.(FieldValue).Value.([]string)
	if !ok || len(acc) == 0 {
		t.Fatalf("expected non-empty []string acceptance, got %#v", res.(FieldValue).Value)
	}
	acc[0] = "CORRUPTED"
	res2, err := RetrievePlan(p, RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	fresh := res2.(FieldValue).Value.([]string)
	if fresh[0] == "CORRUPTED" {
		t.Fatal("MUTATION LEAK: mutating a returned field-level []string value corrupted the canonical Plan's Task.Acceptance via shared backing array")
	}
}

// entityKind garbage input: neither milestone, phase, nor task shape. All four ID-bearing levels
// must reject it with an error, never a panic or a silent empty/zero-value result.
func TestRetrievePlanGarbageIDRejectedAtEveryLevel(t *testing.T) {
	p := validPlan()
	garbage := "not-an-id-at-all"
	for _, lvl := range []RetrievalLevel{LevelMilestone, LevelPhase, LevelTask} {
		if _, err := RetrievePlan(p, RetrieveInput{Level: lvl, ID: garbage}); err == nil {
			t.Fatalf("level %q accepted garbage id %q without error", lvl, garbage)
		}
	}
	if _, err := RetrievePlan(p, RetrieveInput{Level: LevelField, ID: garbage, Field: "name"}); err == nil {
		t.Fatalf("level field accepted garbage id %q without error", garbage)
	}
}

func TestRetrieveExecGarbageIDRejectedAtEveryLevel(t *testing.T) {
	ex := execFixture(t)
	garbage := "not-an-id-at-all"
	for _, lvl := range []RetrievalLevel{LevelMilestone, LevelPhase, LevelTask} {
		if _, err := RetrieveExec(ex, RetrieveInput{Level: lvl, ID: garbage}); err == nil {
			t.Fatalf("level %q accepted garbage id %q without error", lvl, garbage)
		}
	}
	if _, err := RetrieveExec(ex, RetrieveInput{Level: LevelField, ID: garbage, Field: "status"}); err == nil {
		t.Fatalf("level field accepted garbage id %q without error", garbage)
	}
}

// Empty-string level (flag never set) must error like an unknown level, not panic or silently
// default to outline.
func TestRetrievePlanEmptyLevelRejected(t *testing.T) {
	if _, err := RetrievePlan(validPlan(), RetrieveInput{}); err == nil {
		t.Fatal("expected error for empty/unset level")
	}
}

func TestRetrieveExecEmptyLevelRejected(t *testing.T) {
	if _, err := RetrieveExec(execFixture(t), RetrieveInput{}); err == nil {
		t.Fatal("expected error for empty/unset level")
	}
}

// A plan-only id (task) queried at field level with a field name that only exists on
// execution.json's ExecTask (e.g. "commit") must fail cleanly against plan.json — no cross-doc
// field leakage / no panic from a struct that doesn't carry that tag.
func TestRetrievePlanFieldRejectsExecOnlyFieldName(t *testing.T) {
	if _, err := RetrievePlan(validPlan(), RetrieveInput{Level: LevelField, ID: "M1.P1.T1", Field: "commit"}); err == nil {
		t.Fatal("expected error: plan.json Task has no 'commit' field (that's execution.json-only)")
	}
}

// A milestone id queried at field level against execution.json (which has no milestone-scoped
// data) must error, not fall through to matching some unrelated task by accident.
func TestRetrieveExecFieldMilestoneIDNeverMatchesATask(t *testing.T) {
	if _, err := RetrieveExec(execFixture(t), RetrieveInput{Level: LevelField, ID: "M1", Field: "status"}); err == nil {
		t.Fatal("expected error: M1 is a milestone id, execution.json has no milestone-scoped fields")
	}
}

// RetrievalLevel.Known() boundary: every declared constant is known, empty and garbage strings
// are not — guards the CLI validation path this closed set exists to support.
func TestRetrievalLevelKnown(t *testing.T) {
	for _, lvl := range []RetrievalLevel{LevelOutline, LevelMilestone, LevelPhase, LevelTask, LevelField} {
		if !lvl.Known() {
			t.Fatalf("declared level %q reports Known()==false", lvl)
		}
	}
	for _, lvl := range []RetrievalLevel{"", "bogus", "Outline", "OUTLINE"} {
		if RetrievalLevel(lvl).Known() {
			t.Fatalf("level %q must not be Known()", lvl)
		}
	}
}
