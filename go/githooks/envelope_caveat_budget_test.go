package githooks

import (
	"fmt"
	"testing"
)

// TestBuildHookResultCombinedOverflowBudget pins the shared-caveat-budget
// contract across every combination of "does this side overflow": the caveats
// array is a single 50-member resource that both the errors-overflow summary
// and the warn-overflow summary land in, so its budget has to be split, not
// spent twice. Each case states the exact caveat census it expects, so a
// regression that double-spends the budget (51 caveats, record rejected) or
// over-reserves it (a summary slot burned on a side that did not overflow)
// fails here instead of at a caller.
//
// failing findings are uncategorized Secrets; warn findings are categorized
// Secrets demoted by WarnOnCategorizedSecrets - the pairing that makes both
// sides independently unbounded in one run.
func TestBuildHookResultCombinedOverflowBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		// nFailing/nWarn are the finding counts fed in.
		nFailing, nWarn int
		// wantErrors is the errors array size; wantCaveats the caveats array
		// size, which must never exceed maxDiagnostics.
		wantErrors, wantCaveats int
		// wantErrorsOmitted/wantWarnOmitted are the counts each side's
		// overflow-summary caveat must report, or 0 for "no summary caveat
		// for this side at all".
		wantErrorsOmitted, wantWarnOmitted int
	}{
		{
			// The exact confirmed regression: both sides one over the cap.
			// Budget: 1 errors summary + 1 warn summary + 48 warn findings.
			name: "both sides overflow by one", nFailing: 51, nWarn: 51,
			wantErrors: 50, wantCaveats: 50, wantErrorsOmitted: 1, wantWarnOmitted: 3,
		},
		{
			// Same split, far past the cap: the reserved slots do not grow
			// with the overflow size, only the reported counts do.
			name: "both sides overflow by a wide margin", nFailing: 200, nWarn: 300,
			wantErrors: 50, wantCaveats: 50, wantErrorsOmitted: 150, wantWarnOmitted: 252,
		},
		{
			// warn fits the full cap but not the reduced budget, so it is the
			// errors-side reservation alone that forces warn's truncation.
			name: "warn at the cap with failing overflowing", nFailing: 51, nWarn: 50,
			wantErrors: 50, wantCaveats: 50, wantErrorsOmitted: 1, wantWarnOmitted: 2,
		},
		{
			// warn fits the reduced budget exactly: no warn summary, and the
			// errors summary takes the 50th slot.
			name: "warn fits the reduced budget exactly", nFailing: 51, nWarn: 49,
			wantErrors: 50, wantCaveats: 50, wantErrorsOmitted: 1, wantWarnOmitted: 0,
		},
		{
			// failing exactly at the cap does not overflow, so no slot is
			// reserved for it and warn keeps the whole array.
			name: "failing exactly at the cap with warn overflowing", nFailing: 50, nWarn: 51,
			wantErrors: 50, wantCaveats: 50, wantErrorsOmitted: 0, wantWarnOmitted: 2,
		},
		{
			// Only failing overflows: one caveat, the errors summary.
			name: "failing overflows with no warn findings", nFailing: 51, nWarn: 0,
			wantErrors: 50, wantCaveats: 1, wantErrorsOmitted: 1, wantWarnOmitted: 0,
		},
		{
			// Neither side overflows: no summary caveats at all.
			name: "neither side overflows", nFailing: 3, nWarn: 4,
			wantErrors: 3, wantCaveats: 4, wantErrorsOmitted: 0, wantWarnOmitted: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var findings []Finding
			for i := 0; i < tc.nFailing; i++ {
				findings = append(findings, Finding{
					Path:   fmt.Sprintf("fixture-failing-%03d.txt", i),
					Rule:   "fixture-widget-rule",
					Detail: fmt.Sprintf("possible widget leak %03d", i),
				})
			}
			for i := 0; i < tc.nWarn; i++ {
				findings = append(findings, Finding{
					Path:     fmt.Sprintf("fixture-warned-%03d.txt", i),
					Rule:     "fixture-widget-rule",
					Detail:   fmt.Sprintf("abstract-fixture-value-%03d", i),
					Category: "credentials",
				})
			}

			result, err := BuildHookResult([]string{"githooks", "scan"}, ScanOutcome{
				Secrets:                  findings,
				WarnOnCategorizedSecrets: true,
			})
			if err != nil {
				t.Fatalf("BuildHookResult returned an error instead of a record: %v", err)
			}
			if len(result.Errors) != tc.wantErrors {
				t.Fatalf("Errors = %d, want %d", len(result.Errors), tc.wantErrors)
			}
			if len(result.Caveats) > maxDiagnostics {
				t.Fatalf("Caveats = %d, over the hard cap of %d", len(result.Caveats), maxDiagnostics)
			}
			if len(result.Caveats) != tc.wantCaveats {
				t.Fatalf("Caveats = %d, want %d", len(result.Caveats), tc.wantCaveats)
			}

			// Both summaries share one code, so they are told apart by the
			// message's array name - and both must be present, with their own
			// counts, whenever both sides overflowed.
			wantSummaries := map[string]bool{}
			if tc.wantErrorsOmitted > 0 {
				wantSummaries[fmt.Sprintf("%d additional finding(s) omitted from errors (record cap)", tc.wantErrorsOmitted)] = false
			}
			if tc.wantWarnOmitted > 0 {
				wantSummaries[fmt.Sprintf("%d additional finding(s) omitted from caveats (record cap)", tc.wantWarnOmitted)] = false
			}
			summaries := 0
			for _, c := range result.Caveats {
				if c.Code != "caveats.githooks.findings_truncated" {
					continue
				}
				summaries++
				seen, want := wantSummaries[c.Message]
				if !want {
					t.Fatalf("unexpected overflow-summary caveat %q", c.Message)
				}
				if seen {
					t.Fatalf("overflow-summary caveat %q appeared twice", c.Message)
				}
				wantSummaries[c.Message] = true
			}
			if summaries != len(wantSummaries) {
				t.Fatalf("got %d overflow-summary caveat(s), want %d: %v", summaries, len(wantSummaries), wantSummaries)
			}
			for msg, seen := range wantSummaries {
				if !seen {
					t.Fatalf("missing overflow-summary caveat %q", msg)
				}
			}

			assertCanonicalJSON(t, result)
		})
	}
}
