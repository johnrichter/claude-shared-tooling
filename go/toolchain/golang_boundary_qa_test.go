package toolchain

import "testing"

// TestQAVerifyGoTestFailureLocationStopsAtPriorTestBoundary is adversarial:
// two failing tests back to back must each get their own file/line, never
// bleeding a neighbor's log line across the "--- FAIL" boundary.
func TestQAVerifyGoTestFailureLocationStopsAtPriorTestBoundary(t *testing.T) {
	out := []byte("=== RUN   TestA\n    main_test.go:5: bad A\n--- FAIL: TestA (0.00s)\n=== RUN   TestB\n    main_test.go:9: bad B\n--- FAIL: TestB (0.00s)\nFAIL\n")
	diags := parseGoTestFailures(out, "go test")
	if len(diags) != 2 {
		t.Fatalf("parseGoTestFailures = %+v, want 2", diags)
	}
	if diags[0].File != "main_test.go" || diags[0].Line != 5 {
		t.Errorf("diags[0] = %+v, want main_test.go:5", diags[0])
	}
	if diags[1].File != "main_test.go" || diags[1].Line != 9 {
		t.Errorf("diags[1] = %+v, want main_test.go:9", diags[1])
	}
}

// TestQAVerifyGoTestFailureLocationPlainMultiFailAttributesEachOwnLine covers
// the path runE2ETest actually spawns: plain go test (no -v) prints each
// failure's log line immediately after its own "--- FAIL" header, with no
// "=== RUN" separating one failure's trailing logs from the next's header.
// Each failure must still get its own file/line, never the prior failure's.
func TestQAVerifyGoTestFailureLocationPlainMultiFailAttributesEachOwnLine(t *testing.T) {
	out := []byte("--- FAIL: TestA (0.00s)\n    a_test.go:5: bad A\n--- FAIL: TestB (0.00s)\n    b_test.go:9: bad B\nFAIL\n")
	diags := parseGoTestFailures(out, "go test")
	if len(diags) != 2 {
		t.Fatalf("parseGoTestFailures = %+v, want 2", diags)
	}
	if diags[0].File != "a_test.go" || diags[0].Line != 5 {
		t.Errorf("diags[0] = %+v, want a_test.go:5", diags[0])
	}
	if diags[1].File != "b_test.go" || diags[1].Line != 9 {
		t.Errorf("diags[1] = %+v, want b_test.go:9", diags[1])
	}
}

// TestQAVerifyGoTestFailureLocationNoLogLineLeavesZero checks a failing test
// whose own header carries no adjacent file:line log line (e.g. a panic
// recovered by the test harness rather than a t.Fatal) still produces a
// diagnostic — with File/Line left unset rather than borrowing an unrelated
// neighbor's position.
func TestQAVerifyGoTestFailureLocationNoLogLineLeavesZero(t *testing.T) {
	out := []byte("--- FAIL: TestPanic (0.00s)\nFAIL\n")
	diags := parseGoTestFailures(out, "go test")
	if len(diags) != 1 {
		t.Fatalf("parseGoTestFailures = %+v, want 1", diags)
	}
	if diags[0].File != "" || diags[0].Line != 0 {
		t.Errorf("diags[0] = %+v, want no file/line borrowed from nowhere", diags[0])
	}
}
