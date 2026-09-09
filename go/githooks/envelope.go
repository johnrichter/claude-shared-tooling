package githooks

import (
	"fmt"
	"io"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// maxDiagnostics is clikit's per-array cap on errors/caveats (result-
// record.schema.json). A scan run with more findings than this reports the
// first maxDiagnostics as errors and folds the rest into one summarizing
// caveat instead of failing to build a record at all.
const maxDiagnostics = 50

// ScanOutcome is one githooks run's full result, across all three scanners,
// ready for BuildHookResult.
type ScanOutcome struct {
	Secrets         []Finding
	RawBinary       []Finding
	PrivacyFailures []Finding
	PrivacyWarnings []Finding
	// Strict escalates PrivacyWarnings to failures, matching the source
	// guardrail's default-warn/--strict-fails posture.
	Strict bool
	// WarnOnCategorizedSecrets is a severity switch, never an on/off switch:
	// the underlying scan that populates Secrets always runs, unconditionally,
	// regardless of this field. All it changes is where a categorized finding
	// (Finding.Category != "", i.e. betterleaks-sourced credential/PII/
	// financial detection) lands once found: false (the default) keeps
	// today's unchanged behavior, every Secrets finding failing; true demotes
	// only the categorized ones to non-blocking caveats, so a repo can roll
	// the new detection out observe-first before flipping it to blocking.
	// A finding with an empty Category (ScanSecrets' pre-existing hand-rolled
	// patterns) is never affected by this field, in either direction - it
	// stays a hard failure exactly as before this field existed.
	//
	// This is independent of Strict: Strict only ever escalates
	// PrivacyWarnings, and this field only ever demotes categorized Secrets -
	// neither flag reaches into the other's finding class, so setting both at
	// once does not re-escalate a categorized secret back to failing.
	WarnOnCategorizedSecrets bool
}

// BuildHookResult composes outcome into the one clikit result record a
// githooks run emits: success when clean, caveats when only non-blocking
// findings were found (privacy warnings, or - under
// WarnOnCategorizedSecrets - categorized secrets, or both, with Strict
// false), precondition_unmet (the commit precondition "no guardrail
// violation is staged" is not met) with one governing diagnostic per finding
// otherwise.
//
// Every finding-derived diagnostic's context carries the same three keys -
// path, rule, category - on both the caveats and the errors path. category
// is the empty string for finding kinds outside Finding.Category's taxonomy,
// never absent, so a consumer can read it unconditionally.
func BuildHookResult(command []string, outcome ScanOutcome) (*clikit.Result, error) {
	data := map[string]any{
		"secrets_found":            len(outcome.Secrets),
		"raw_binaries_found":       len(outcome.RawBinary),
		"privacy_violations_found": len(outcome.PrivacyFailures),
		"privacy_warnings_found":   len(outcome.PrivacyWarnings),
	}

	// Split Secrets on Category, not on any other property: an uncategorized
	// finding (ScanSecrets' own patterns) is always a failure; a categorized
	// one (betterleaks-sourced) is a failure unless WarnOnCategorizedSecrets
	// demotes it to a caveat instead.
	var categorized, uncategorized []Finding
	for _, f := range outcome.Secrets {
		if f.Category != "" {
			categorized = append(categorized, f)
		} else {
			uncategorized = append(uncategorized, f)
		}
	}

	const removeAndRecommit = "remove the offending content from the flagged path and re-commit"

	var failing []taggedFinding
	failing = append(failing, taggedFindings(uncategorized, "precondition_unmet.secret_detected", removeAndRecommit)...)
	if !outcome.WarnOnCategorizedSecrets {
		failing = append(failing, taggedFindings(categorized, "precondition_unmet.secret_detected", removeAndRecommit)...)
	}
	failing = append(failing, taggedFindings(outcome.RawBinary, "precondition_unmet.raw_binary_detected", removeAndRecommit)...)
	failing = append(failing, taggedFindings(outcome.PrivacyFailures, "precondition_unmet.privacy_violation", removeAndRecommit)...)
	if outcome.Strict {
		failing = append(failing, taggedFindings(outcome.PrivacyWarnings, "precondition_unmet.privacy_violation", removeAndRecommit)...)
	}

	var warn []taggedFinding
	if outcome.WarnOnCategorizedSecrets {
		warn = append(warn, taggedFindings(categorized, "caveats.githooks.categorized_secret_detected",
			"confirm the flagged content is not a real credential/PII/financial value; remove it if it is, or extend the scanner's allowlist if it is a verified false positive")...)
	}
	if !outcome.Strict {
		warn = append(warn, taggedFindings(outcome.PrivacyWarnings, "caveats.privacy.internal_identifier",
			"confirm the flagged mention is not real internal leakage; rephrase or remove it if it is")...)
	}

	if len(failing) == 0 {
		if len(warn) == 0 {
			return clikit.NewSuccess(command, data)
		}
		// No failing findings, so no errors-overflow summary can compete for
		// room here: warn gets the whole caveats array to itself.
		caveats, err := warningCaveats(warn, maxDiagnostics)
		if err != nil {
			return nil, err
		}
		return clikit.NewCaveats(command, data, caveats)
	}

	// warn's own caveats are built the same way whether or not there are
	// also failures: a categorized-secret warning is never dropped just
	// because an unrelated finding elsewhere in the same run is failing.
	//
	// Two independent sources can each overflow in the same call - failing
	// past the errors cap, warn past the caveats cap - and each one's
	// overflow-summary caveat lands in the same single caveats array, whose
	// own cap is maxDiagnostics. So the summary slot the failing side needs is
	// reserved out of the caveats budget *before* warn's caveats are built,
	// and warn is truncated within whatever budget is left. Appending the
	// failing-side summary onto an already-full warn-derived slice instead
	// produced maxDiagnostics+1 members and a record clikit rejects outright.
	// Each side that actually overflows still reports its own count, because
	// each side gets its own summary slot only when it truly overflows: the
	// caller learns both how many errors and how many caveats were dropped.
	failingOverflow := 0
	if len(failing) > maxDiagnostics {
		failingOverflow = len(failing) - maxDiagnostics
		failing = failing[:maxDiagnostics]
	}
	caveatBudget := maxDiagnostics
	if failingOverflow > 0 {
		caveatBudget--
	}
	caveats, err := warningCaveats(warn, caveatBudget)
	if err != nil {
		return nil, err
	}
	if failingOverflow > 0 {
		cv, err := clikit.NewCaveat("caveats.githooks.findings_truncated",
			fmt.Sprintf("%d additional finding(s) omitted from errors (record cap)", failingOverflow),
			clikit.Manual("re-run the underlying scanner directly for the full list"), nil)
		if err != nil {
			return nil, err
		}
		caveats = append(caveats, cv)
	}

	errs := make([]clikit.Diagnostic, 0, len(failing))
	for _, tf := range failing {
		e, err := clikit.NewError(tf.code, tf.Detail,
			clikit.Manual(tf.remediation),
			map[string]any{"path": tf.Path, "rule": tf.Rule, "category": tf.Category})
		if err != nil {
			return nil, err
		}
		errs = append(errs, e)
	}
	return clikit.NewPreconditionUnmet(command, data, errs, caveats)
}

// EmitHookResult builds outcome's result record and writes it to w as the
// canonical JSON envelope, returning the exit code the caller's process
// should exit with.
func EmitHookResult(w io.Writer, command []string, outcome ScanOutcome) (int, error) {
	result, err := BuildHookResult(command, outcome)
	if err != nil {
		return 0, err
	}
	if err := clikit.Emit(w, result); err != nil {
		return 0, err
	}
	return result.ExitCode, nil
}

// warningCaveats renders warn as clikit caveats, one per finding, in at most
// budget members - the caller's share of the record's 50-caveat cap, reduced
// when something else (the errors-overflow summary) also needs room in that
// same array. An overflow past budget folds into one summarizing caveat that
// shares the array rather than living in a separate one, so it reserves a
// slot up front instead of being appended after a full-width truncation.
func warningCaveats(warn []taggedFinding, budget int) ([]clikit.Diagnostic, error) {
	// Unreachable at maxDiagnostics=50 (the smallest budget any caller passes
	// is 49); guarded so a future cap change degrades to "no caveats" rather
	// than a negative slice bound.
	if budget <= 0 {
		return nil, nil
	}
	var caveats []clikit.Diagnostic
	if len(warn) > budget {
		overflow := len(warn) - (budget - 1)
		warn = warn[:budget-1]
		cv, err := clikit.NewCaveat("caveats.githooks.findings_truncated",
			fmt.Sprintf("%d additional finding(s) omitted from caveats (record cap)", overflow),
			clikit.Manual("re-run the underlying scanner directly for the full list"), nil)
		if err != nil {
			return nil, err
		}
		caveats = append(caveats, cv)
	}
	for _, tf := range warn {
		cv, err := clikit.NewCaveat(tf.code, tf.Detail,
			clikit.Manual(tf.remediation),
			map[string]any{"path": tf.Path, "rule": tf.Rule, "category": tf.Category})
		if err != nil {
			return nil, err
		}
		caveats = append(caveats, cv)
	}
	return caveats, nil
}

// taggedFinding pairs a Finding with the diagnostic code and remediation
// text its governing class falls under, whether it ends up an error or a
// caveat.
type taggedFinding struct {
	Finding
	code        string
	remediation string
}

func taggedFindings(findings []Finding, code, remediation string) []taggedFinding {
	tagged := make([]taggedFinding, len(findings))
	for i, f := range findings {
		tagged[i] = taggedFinding{Finding: f, code: code, remediation: remediation}
	}
	return tagged
}
