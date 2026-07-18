package bh

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

// ---- adversarial tests for FileSurface (AC1-3) ----

// AC1: FileSurface field with correct json tag
func TestFileSurfaceFieldExists(t *testing.T) {
	task := Task{
		ID:           "M1.P1.T1",
		Summary:      "s",
		Deliverable:  "d",
		Model:        ModelSonnet46,
		Effort:       EffortMedium,
		TestStrategy: "unit",
		Acceptance:   []string{"a"},
		FileSurface:  []FileSurfaceEntry{{Path: "pkg/*.go"}, {Path: "cmd/*.go"}},
	}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m["file_surface"]
	if !ok {
		t.Fatal("file_surface not present in JSON output — json tag incorrect or field absent")
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("file_surface should be array of 2, got %v", v)
	}
}

// AC1: omitempty — empty FileSurface must NOT appear in JSON
func TestFileSurfaceOmitEmpty(t *testing.T) {
	task := Task{
		ID: "M1.P1.T1", Summary: "s", Deliverable: "d",
		Model: ModelSonnet46, Effort: EffortMedium,
		TestStrategy: "unit", Acceptance: []string{"a"},
	}
	b, _ := json.Marshal(task)
	if strings.Contains(string(b), "file_surface") {
		t.Fatalf("empty FileSurface must be omitted from JSON (omitempty), got: %s", b)
	}
}

// AC2: code task WITH file_surface -> ok=true, NO file_surface warning
func TestFileSurfacePresentCodeTask(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"],"file_surface":[{"path":"bh/*.go"}]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("expected ok=true, got errors: %v", res.Errors)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "file_surface") && strings.Contains(w, "M1.P1.T1") {
			t.Fatalf("must NOT warn about file_surface when it is present; warnings: %v", res.Warnings)
		}
	}
}

// AC2: code task WITHOUT file_surface -> ok=true (not an error), but warning present
func TestFileSurfaceAbsentCodeTaskWarningNotError(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("missing file_surface must NOT set ok=false; errors: %v", res.Errors)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("missing file_surface must produce ZERO errors; got: %v", res.Errors)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "file_surface") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning mentioning file_surface; got warnings: %v", res.Warnings)
	}
}

// AC2: docs task WITHOUT file_surface -> ok=true, no file_surface warning
func TestFileSurfaceAbsentDocsTaskNoWarning(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","deliverable_kind":"docs","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("docs task without file_surface must validate ok; errors: %v", res.Errors)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "file_surface") {
			t.Fatalf("docs task must NOT produce file_surface warning; warnings: %v", res.Warnings)
		}
	}
}

// AC2: empty kind (defaults to code) WITHOUT file_surface -> warning present
func TestFileSurfaceAbsentEmptyKindCodeDefault(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("ok must be true even without file_surface; errors: %v", res.Errors)
	}
	warnCount := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "file_surface") && strings.Contains(w, "M1.P1.T1") {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Fatalf("expected exactly 1 file_surface warning for the task, got %d; warnings: %v", warnCount, res.Warnings)
	}
}

// AC2: file_surface with empty array in JSON -> should still warn (empty array != populated)
func TestFileSurfaceEmptyArrayInJSON(t *testing.T) {
	// JSON-level empty array unmarshals to nil/empty slice -> treated as absent
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"],"file_surface":[]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("ok must be true for empty file_surface array; errors: %v", res.Errors)
	}
	// empty array == len 0 == absent for the guard logic; warning expected
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "file_surface") && strings.Contains(w, "M1.P1.T1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected file_surface warning for empty array (same as absent); warnings: %v", res.Warnings)
	}
}

// AC2: multiple tasks — only code tasks without file_surface get warned
func TestFileSurfaceWarnPerTask(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"],"file_surface":[{"path":"bh/*.go"}]},{"id":"M1.P1.T2","name":"Task two","summary":"s2","deliverable":"d2","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"]},{"id":"M1.P1.T3","name":"Task three","summary":"s3","deliverable":"d3","deliverable_kind":"docs","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("expected ok=true; errors: %v", res.Errors)
	}
	t1Warned, t2Warned, t3Warned := false, false, false
	for _, w := range res.Warnings {
		if strings.Contains(w, "file_surface") {
			if strings.Contains(w, "M1.P1.T1") {
				t1Warned = true
			}
			if strings.Contains(w, "M1.P1.T2") {
				t2Warned = true
			}
			if strings.Contains(w, "M1.P1.T3") {
				t3Warned = true
			}
		}
	}
	if t1Warned {
		t.Fatal("T1 has file_surface set — must NOT warn")
	}
	if !t2Warned {
		t.Fatal("T2 is a code task without file_surface — must warn")
	}
	if t3Warned {
		t.Fatal("T3 is a docs task — must NOT warn regardless of file_surface absence")
	}
}

// Typed-shape validation: an empty path is a schema-shape error.
func TestFileSurfaceEmptyPathIsError(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"],"file_surface":[{"path":""}]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if res.OK {
		t.Fatal("empty file_surface path must be a validation error")
	}
}

// Typed-shape validation: an unknown kind is a schema-shape error.
func TestFileSurfaceUnknownKindIsError(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"],"file_surface":[{"path":"bh/*.go","kind":"bogus"}]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if res.OK {
		t.Fatal("unknown file_surface kind must be a validation error")
	}
}

// AC3: JSON round-trip preserves FileSurface values
func TestFileSurfaceRoundTrip(t *testing.T) {
	original := Task{
		ID: "M1.P1.T1", Summary: "s", Deliverable: "d",
		Model: ModelSonnet46, Effort: EffortMedium,
		TestStrategy: "unit", Acceptance: []string{"a"},
		FileSurface: []FileSurfaceEntry{
			{Path: "bh/types.go", Required: true, Kind: FSFile},
			{Path: "bh/plan.go"},
			{Path: "cmd/main.go", Kind: FSGlob},
		},
	}
	b, _ := json.Marshal(original)
	var recovered Task
	if err := json.Unmarshal(b, &recovered); err != nil {
		t.Fatal(err)
	}
	if len(recovered.FileSurface) != 3 {
		t.Fatalf("round-trip lost FileSurface entries; got %v", recovered.FileSurface)
	}
	for i, want := range original.FileSurface {
		if recovered.FileSurface[i] != want {
			t.Fatalf("FileSurface[%d] = %+v, want %+v", i, recovered.FileSurface[i], want)
		}
	}
}

// Backward compat: a bare-string file_surface entry (the legacy shape every existing
// plan.json still uses) must decode to {Path:s, Required:false, Kind:""}, so the typed-surface
// binary can still read old plans with no migration. A plan mixing string and object entries in
// one array must also parse — encoding/json dispatches UnmarshalJSON per element.
func TestFileSurfaceLegacyStringForm(t *testing.T) {
	var e FileSurfaceEntry
	if err := json.Unmarshal([]byte(`"docs/projects/foo/spike.md"`), &e); err != nil {
		t.Fatalf("bare-string file_surface entry must decode, got err: %v", err)
	}
	want := FileSurfaceEntry{Path: "docs/projects/foo/spike.md"}
	if e != want {
		t.Fatalf("legacy string decode = %+v, want %+v", e, want)
	}
	if e.Kind.Resolve() != FSFile {
		t.Fatalf("legacy string entry must resolve to the file default, got %q", e.Kind.Resolve())
	}

	var mixed struct {
		FS []FileSurfaceEntry `json:"file_surface"`
	}
	if err := json.Unmarshal([]byte(`{"file_surface":["bh/plan.go",{"path":"bh/*.go","kind":"glob","required":true}]}`), &mixed); err != nil {
		t.Fatalf("mixed string+object file_surface array must decode, got err: %v", err)
	}
	if len(mixed.FS) != 2 {
		t.Fatalf("mixed array should yield 2 entries, got %d", len(mixed.FS))
	}
	if mixed.FS[0] != (FileSurfaceEntry{Path: "bh/plan.go"}) {
		t.Fatalf("mixed[0] string form = %+v, want {Path:bh/plan.go}", mixed.FS[0])
	}
	if mixed.FS[1] != (FileSurfaceEntry{Path: "bh/*.go", Kind: FSGlob, Required: true}) {
		t.Fatalf("mixed[1] object form = %+v", mixed.FS[1])
	}
}

// A whole plan that uses plain-string file_surface throughout (every existing plan.json) must
// pass ValidatePlanBytes — this is the exact shape the orchestrator's retrieve/batch/record path
// feeds the binary. Guards against the typed-surface change ever hard-failing legacy plans again.
func TestValidatePlanLegacyStringFileSurface(t *testing.T) {
	raw := []byte(`{"goal":"g","success_criteria":["s"],"milestones":[{"id":"M1","name":"n","phases":[{"id":"M1.P1","name":"p","tasks":[{"id":"M1.P1.T1","name":"Task one","summary":"s","deliverable":"d","model":"claude-sonnet-4-6","effort":"medium","test_strategy":"t","acceptance":["a"],"file_surface":["bh/plan.go","bh/types.go"]}]}]}]}`)
	res := ValidatePlanBytes(raw)
	if !res.OK {
		t.Fatalf("legacy plain-string file_surface plan must validate, got errors: %v", res.Errors)
	}
}

// ---- bidirectional re-assertion adversarial fixtures ----
//
// Both fixtures below reference VerifyChangedSetSubsetOfSurface / VerifyMergedSurface (batch.go),
// exercising the reverse-direction and post-merge checks end to end.

// Fixture 1 — off-surface write (reverse direction, sites 2/3). A task declares its surface as
// "tests/*.go" only; the agent instead wrote its test into "src/foo_test.go" — present
// on disk, so a forward-only check (VerifyFileSurface) never sees a problem. Only the reverse
// direction (changed-set ⊆ surface) flags the off-surface path; a path the surface DOES cover is
// never flagged alongside it.
func TestAdv_OffSurfaceWriteFailsChangedSetCheck(t *testing.T) {
	entries := []FileSurfaceEntry{{Path: "tests/*.go", Kind: FSGlob, Required: true}}
	changed := []string{"tests/foo_test.go", "src/foo_test.go"} // test literally landed in src/, not tests/
	res := VerifyChangedSetSubsetOfSurface(changed, entries)
	if res.OK {
		t.Fatal("a changed path outside the declared surface must fail the reverse (changed-set ⊆ surface) check")
	}
	if len(res.OffSurface) != 1 || res.OffSurface[0] != "src/foo_test.go" {
		t.Fatalf("expected exactly src/foo_test.go flagged off-surface, got %v", res.OffSurface)
	}
	// A wholly on-surface changed-set must pass — the check does not false-positive on covered paths.
	if r2 := VerifyChangedSetSubsetOfSurface([]string{"tests/foo_test.go"}, entries); !r2.OK {
		t.Fatalf("a changed-set fully covered by the surface must pass, got violations: %v", r2.OffSurface)
	}
}

// Fixture 2 — dropped-at-merge artifact (forward direction, site 3). Two tasks' branches are
// octopus-merged; task B's required deliverable was present in ITS OWN pre-merge worktree (a
// pre-commit-only check would have passed) but is absent from the merged tree (the octopus merge
// silently dropped it. Only the post-merge union re-assertion
// (VerifyMergedSurface, checked against the merged tree) catches it.
func TestAdv_DroppedAtMergeArtifactFailsPostMergeReassertion(t *testing.T) {
	taskA := []FileSurfaceEntry{{Path: "pkg/a.go", Required: true}}
	taskB := []FileSurfaceEntry{{Path: "pkg/b.go", Required: true}}

	// Each task's own pre-merge worktree has its own deliverable — the pre-commit site (site 2)
	// passes for both, independently.
	preMergeA := fstest.MapFS{"pkg/a.go": {Data: []byte("package pkg\n")}}
	preMergeB := fstest.MapFS{"pkg/b.go": {Data: []byte("package pkg\n")}}
	if r := VerifyFileSurface(preMergeA, taskA); !r.OK {
		t.Fatalf("task A's own pre-merge worktree must satisfy its own surface, got: %v", r.Violations)
	}
	if r := VerifyFileSurface(preMergeB, taskB); !r.OK {
		t.Fatalf("task B's own pre-merge worktree must satisfy its own surface, got: %v", r.Violations)
	}

	// The merge silently drops pkg/b.go (e.g. an add/add resolution that kept only one side).
	mergedTree := fstest.MapFS{"pkg/a.go": {Data: []byte("package pkg\n")}}
	res := VerifyMergedSurface(mergedTree, [][]FileSurfaceEntry{taskA, taskB})
	if res.OK {
		t.Fatal("a required deliverable dropped by the merge must fail the post-merge union re-assertion")
	}
	found := false
	for _, v := range res.Violations {
		if v.Path == "pkg/b.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected pkg/b.go flagged as the dropped-at-merge violation, got: %v", res.Violations)
	}

	// A merge that preserves both deliverables must pass the same union re-assertion.
	fullyMergedTree := fstest.MapFS{
		"pkg/a.go": {Data: []byte("package pkg\n")},
		"pkg/b.go": {Data: []byte("package pkg\n")},
	}
	if r := VerifyMergedSurface(fullyMergedTree, [][]FileSurfaceEntry{taskA, taskB}); !r.OK {
		t.Fatalf("a merge preserving every task's deliverable must pass; got: %v", r.Violations)
	}
}
