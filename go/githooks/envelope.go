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
}

// BuildHookResult composes outcome into the one clikit result record a
// githooks run emits: success when clean, caveats when only non-blocking
// privacy warnings were found (and Strict is false), precondition_unmet
// (the commit precondition "no guardrail violation is staged" is not met)
// with one governing diagnostic per finding otherwise.
func BuildHookResult(command []string, outcome ScanOutcome) (*clikit.Result, error) {
	data := map[string]any{
		"secrets_found":            len(outcome.Secrets),
		"raw_binaries_found":       len(outcome.RawBinary),
		"privacy_violations_found": len(outcome.PrivacyFailures),
		"privacy_warnings_found":   len(outcome.PrivacyWarnings),
	}

	var failing []taggedFinding
	failing = append(failing, taggedFindings(outcome.Secrets, "precondition_unmet.secret_detected")...)
	failing = append(failing, taggedFindings(outcome.RawBinary, "precondition_unmet.raw_binary_detected")...)
	failing = append(failing, taggedFindings(outcome.PrivacyFailures, "precondition_unmet.privacy_violation")...)
	if outcome.Strict {
		failing = append(failing, taggedFindings(outcome.PrivacyWarnings, "precondition_unmet.privacy_violation")...)
	}

	if len(failing) == 0 {
		if len(outcome.PrivacyWarnings) == 0 {
			return clikit.NewSuccess(command, data)
		}
		caveats := make([]clikit.Diagnostic, 0, len(outcome.PrivacyWarnings))
		for _, f := range outcome.PrivacyWarnings {
			cv, err := clikit.NewCaveat("caveats.privacy.internal_identifier", f.Detail,
				clikit.Manual("confirm the flagged mention is not real internal leakage; rephrase or remove it if it is"),
				map[string]any{"path": f.Path, "rule": f.Rule})
			if err != nil {
				return nil, err
			}
			caveats = append(caveats, cv)
		}
		return clikit.NewCaveats(command, data, caveats)
	}

	var caveats []clikit.Diagnostic
	if len(failing) > maxDiagnostics {
		overflow := len(failing) - maxDiagnostics
		failing = failing[:maxDiagnostics]
		cv, err := clikit.NewCaveat("caveats.githooks.findings_truncated",
			fmt.Sprintf("%d additional finding(s) omitted from errors (record cap)", overflow),
			clikit.Manual("re-run the underlying scanner directly for the full list"), nil)
		if err != nil {
			return nil, err
		}
		caveats = append(caveats, cv)
	}

	errs := make([]clikit.Diagnostic, 0, len(failing))
	for _, tf := range failing {
		e, err := clikit.NewError(tf.code, tf.Detail,
			clikit.Manual("remove the offending content from the flagged path and re-commit"),
			map[string]any{"path": tf.Path, "rule": tf.Rule})
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

// taggedFinding pairs a Finding with the diagnostic code its governing class
// falls under.
type taggedFinding struct {
	Finding
	code string
}

func taggedFindings(findings []Finding, code string) []taggedFinding {
	tagged := make([]taggedFinding, len(findings))
	for i, f := range findings {
		tagged[i] = taggedFinding{Finding: f, code: code}
	}
	return tagged
}
