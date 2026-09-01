package githooks

import (
	"bytes"
	"fmt"
	"testing"
)

// TestVerifyWarnOnCategorizedSecretsCombinedCaveatsCapAt50 is the
// adversarial check for the risk the implementer flagged: with
// WarnOnCategorizedSecrets true, caveats can now be populated from two
// independent finding sources at once (PrivacyWarnings and demoted
// categorized Secrets) in the same call, where before this commit only one
// source could ever reach that array. This constructs 30 of each (60 total,
// well past maxDiagnostics=50) and asserts the combined caveats array is
// capped at exactly 50, with the 50th slot the overflow-summary caveat -
// the same invariant the package's own earlier caveats-truncation fix
// established for a single source.
func TestVerifyWarnOnCategorizedSecretsCombinedCaveatsCapAt50(t *testing.T) {
	const nEach = 30 // 30 + 30 = 60, i.e. 10 over the 50 cap

	var privacyWarnings, categorizedSecrets []Finding
	for i := 0; i < nEach; i++ {
		privacyWarnings = append(privacyWarnings, Finding{
			Path:   fmt.Sprintf("privacy-%02d.md", i),
			Rule:   "internal_identifier",
			Detail: fmt.Sprintf("internal identifier — fixture-widget-rule-%02d", i),
		})
		categorizedSecrets = append(categorizedSecrets, Finding{
			Path:     fmt.Sprintf("secret-%02d.txt", i),
			Rule:     "fixture-widget-rule",
			Detail:   fmt.Sprintf("abstract-fixture-value-%02d", i),
			Category: "credentials",
		})
	}

	outcome := ScanOutcome{
		PrivacyWarnings:          privacyWarnings,
		Secrets:                  categorizedSecrets,
		WarnOnCategorizedSecrets: true,
		// No failing findings and Strict false: every one of the 60
		// findings above is routed through the caveats-only path
		// (`len(failing) == 0` branch), the exact scenario the risk
		// description calls out.
	}

	result, err := BuildHookResult([]string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("BuildHookResult returned an error instead of a record: %v (this would itself indicate the >50 cap was exceeded and clikit's own Validate() rejected the record)", err)
	}

	if result.ExitCode != 10 {
		t.Fatalf("ExitCode = %d, want 10 (caveats)", result.ExitCode)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Errors = %d, want 0 (no failing findings in this outcome)", len(result.Errors))
	}

	got := len(result.Caveats)
	if got != maxDiagnostics {
		t.Fatalf("Caveats = %d, want exactly %d (49 findings + 1 overflow-summary caveat); "+
			"60 combined findings (30 PrivacyWarnings + 30 categorized Secrets) must still cap at %d",
			got, maxDiagnostics, maxDiagnostics)
	}

	// Confirm the reserved slot is genuinely the overflow-summary caveat,
	// not just a 50th ordinary finding caveat that happens to make the
	// count come out right.
	var sawOverflow bool
	for _, c := range result.Caveats {
		if c.Code == "caveats.githooks.findings_truncated" {
			sawOverflow = true
			wantDetail := "11 additional finding(s) omitted from caveats (record cap)" // 60 - 49 = 11
			if c.Message != wantDetail {
				t.Fatalf("overflow caveat Message = %q, want %q", c.Message, wantDetail)
			}
		}
	}
	if !sawOverflow {
		t.Fatalf("no caveats.githooks.findings_truncated caveat found among %d caveats; combined-source overflow was not summarized", got)
	}

	assertCanonicalJSON(t, result)
}

// TestVerifyWarnOnCategorizedSecretsCombinedCaveatsCapWithFailingToo repeats
// the same combined-source overflow, but alongside a separate failing
// (uncategorized) finding, exercising the other branch that calls
// warningCaveats -- the precondition_unmet path -- to confirm the same cap
// holds there too.
func TestVerifyWarnOnCategorizedSecretsCombinedCaveatsCapWithFailingToo(t *testing.T) {
	const nEach = 30

	var privacyWarnings, categorizedSecrets []Finding
	for i := 0; i < nEach; i++ {
		privacyWarnings = append(privacyWarnings, Finding{
			Path:   fmt.Sprintf("privacy-%02d.md", i),
			Rule:   "internal_identifier",
			Detail: fmt.Sprintf("internal identifier — fixture-widget-rule-%02d", i),
		})
		categorizedSecrets = append(categorizedSecrets, Finding{
			Path:     fmt.Sprintf("secret-%02d.txt", i),
			Rule:     "fixture-widget-rule",
			Detail:   fmt.Sprintf("abstract-fixture-value-%02d", i),
			Category: "credentials",
		})
	}

	outcome := ScanOutcome{
		PrivacyWarnings: privacyWarnings,
		Secrets: append([]Finding{
			{Path: "uncategorized.txt", Rule: "fixture-widget-rule", Detail: "possible widget leak, uncategorized"},
		}, categorizedSecrets...),
		WarnOnCategorizedSecrets: true,
	}

	result, err := BuildHookResult([]string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("BuildHookResult: %v", err)
	}
	if result.ExitCode != 30 {
		t.Fatalf("ExitCode = %d, want 30 (precondition_unmet, the uncategorized finding still fails)", result.ExitCode)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %d, want 1 (only the uncategorized finding)", len(result.Errors))
	}
	if got := len(result.Caveats); got != maxDiagnostics {
		t.Fatalf("Caveats = %d, want exactly %d even when failing findings are also present", got, maxDiagnostics)
	}
	assertCanonicalJSON(t, result)
}

// TestVerifyDefaultCompatibilityByteIdentical is an independent
// (implementer-unassisted) construction of the byte-identical compatibility
// claim: a realistic mixed outcome (uncategorized Secrets, categorized
// Secrets, and PrivacyWarnings all present together) run once with
// WarnOnCategorizedSecrets left at its zero value and once with it set
// explicitly to false must produce byte-identical canonical JSON.
func TestVerifyDefaultCompatibilityByteIdentical(t *testing.T) {
	build := func() ScanOutcome {
		return ScanOutcome{
			Secrets: []Finding{
				{Path: "old.txt", Rule: "fixture-widget-rule", Detail: "possible widget leak"},
				{Path: "new.txt", Rule: "fixture-widget-rule", Detail: "abstract-fixture-value", Category: "credentials"},
			},
			PrivacyWarnings: []Finding{
				{Path: "note.md", Rule: "internal_identifier", Detail: "internal identifier — fixture-widget-rule"},
			},
		}
	}

	zeroValue := build() // WarnOnCategorizedSecrets left unset (Go zero value: false)
	explicitFalse := build()
	explicitFalse.WarnOnCategorizedSecrets = false

	command := []string{"githooks", "scan"}
	r1, err := BuildHookResult(command, zeroValue)
	if err != nil {
		t.Fatalf("BuildHookResult(zero value): %v", err)
	}
	r2, err := BuildHookResult(command, explicitFalse)
	if err != nil {
		t.Fatalf("BuildHookResult(explicit false): %v", err)
	}

	j1, err := r1.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical(r1): %v", err)
	}
	j2, err := r2.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical(r2): %v", err)
	}
	if !bytes.Equal(j1, j2) {
		t.Fatalf("zero-value vs explicit-false diverge:\nzero:    %s\nexplicit: %s", j1, j2)
	}

	// Both both-categorized-and-uncategorized findings must fail (default
	// posture unchanged), and the PrivacyWarnings caveat must still be
	// present per the "warnings never dropped" fix (item 5 below) even
	// though there are failing findings in the same run.
	if r1.ExitCode != 30 {
		t.Fatalf("ExitCode = %d, want 30", r1.ExitCode)
	}
	if len(r1.Errors) != 2 {
		t.Fatalf("Errors = %d, want 2 (both Secrets findings fail under default posture)", len(r1.Errors))
	}
	if len(r1.Caveats) != 1 {
		t.Fatalf("Caveats = %d, want 1 (the PrivacyWarnings finding, not dropped)", len(r1.Caveats))
	}
	if r1.Caveats[0].Context["path"] != "note.md" {
		t.Fatalf("Caveats[0].Context[path] = %v, want %q", r1.Caveats[0].Context["path"], "note.md")
	}
}

// TestVerifyPrivacyWarningsCaveatNeverDroppedAlongsideFailure is a direct,
// implementer-unassisted test of the "previously-dropped-caveats" fix
// claim: a ScanOutcome with a failing finding (independent of Secrets/
// WarnOnCategorizedSecrets entirely -- RawBinary here) plus a PrivacyWarnings
// finding must surface the warning as a caveat on the resulting
// precondition_unmet record, not silently drop it. Reading the pre-commit
// source (see report) confirms the old code's caveats slice was declared
// fresh (`var caveats []clikit.Diagnostic`) inside the `len(failing) > 0`
// branch and was never populated from PrivacyWarnings at all in that branch
// -- this is not a hypothetical, it is what the diff replaced.
func TestVerifyPrivacyWarningsCaveatNeverDroppedAlongsideFailure(t *testing.T) {
	outcome := ScanOutcome{
		RawBinary:       []Finding{{Path: "blob.bin", Rule: "raw_binary", Detail: "raw binary content detected"}},
		PrivacyWarnings: []Finding{{Path: "note.md", Rule: "internal_identifier", Detail: "internal identifier — fixture-widget-rule"}},
	}
	result, err := BuildHookResult([]string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("BuildHookResult: %v", err)
	}
	if result.ExitCode != 30 {
		t.Fatalf("ExitCode = %d, want 30 (precondition_unmet, RawBinary fails)", result.ExitCode)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors = %d, want 1", len(result.Errors))
	}
	if len(result.Caveats) != 1 {
		t.Fatalf("Caveats = %d, want 1 (the PrivacyWarnings finding must NOT be dropped just because RawBinary is also failing)", len(result.Caveats))
	}
	if result.Caveats[0].Context["path"] != "note.md" {
		t.Fatalf("Caveats[0].Context[path] = %v, want %q", result.Caveats[0].Context["path"], "note.md")
	}
	assertCanonicalJSON(t, result)
}

// TestVerifyCombinedFailingOverflowPlusWarnOverflowExceedsFiftyCaveats is the
// precise adversarial case the implementer's hand-off note actually
// describes (distinct from the two combined-warn-source tests above): when
// BOTH the failing set AND the warn set separately exceed maxDiagnostics in
// the SAME call, warningCaveats(warn) already returns a caveats slice
// capped at maxDiagnostics (49 real + 1 warn-overflow-summary), and then the
// failing>maxDiagnostics branch appends a SECOND, distinct overflow-summary
// caveat ("...omitted from errors...") on top of that already-full slice.
// Asserted expectation: BuildHookResult must still produce a valid record
// with caveats capped at maxDiagnostics, same as every other branch's
// invariant. THIS TEST IS EXPECTED TO FAIL against the current
// implementation, which stacks a second overflow caveat on top of an
// already-full warn-derived caveats array and produces 51 -- one over cap
// -- causing BuildHookResult to return an error (clikit.Validate rejects
// >50 caveats) instead of a result record. Do not weaken this assertion to
// "make it pass"; the fix belongs in envelope.go's overflow-caveat
// accounting, not here.
func TestVerifyCombinedFailingOverflowPlusWarnOverflowExceedsFiftyCaveats(t *testing.T) {
	const n = 51 // one over maxDiagnostics in both failing and warn

	var uncategorizedSecrets, categorizedSecrets []Finding
	for i := 0; i < n; i++ {
		uncategorizedSecrets = append(uncategorizedSecrets, Finding{
			Path:   fmt.Sprintf("uncategorized-%02d.txt", i),
			Rule:   "fixture-widget-rule",
			Detail: fmt.Sprintf("possible widget leak %02d", i),
		})
		categorizedSecrets = append(categorizedSecrets, Finding{
			Path:     fmt.Sprintf("categorized-%02d.txt", i),
			Rule:     "fixture-widget-rule",
			Detail:   fmt.Sprintf("abstract-fixture-value-%02d", i),
			Category: "credentials",
		})
	}

	outcome := ScanOutcome{
		Secrets:                  append(uncategorizedSecrets, categorizedSecrets...), // 51 failing (uncategorized) + 51 warn (categorized)
		WarnOnCategorizedSecrets: true,
	}

	result, err := BuildHookResult([]string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("BuildHookResult returned an error instead of a valid record: %v -- "+
			"this confirms the combined failing-overflow + warn-overflow case produces more than %d caveats "+
			"(warningCaveats already caps warn at %d, then the failing-overflow branch appends one more on top)",
			err, maxDiagnostics, maxDiagnostics)
	}
	if len(result.Caveats) > maxDiagnostics {
		t.Fatalf("Caveats = %d, exceeds cap of %d -- confirmed regression, and clikit.Validate did not even catch it", len(result.Caveats), maxDiagnostics)
	}
}
