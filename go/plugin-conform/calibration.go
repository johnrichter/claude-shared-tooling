package plugin_conform

import "fmt"

// CalibrationBlockedError names a deterministic-conformance Report with at least one error --
// the mandatory calibration-first gate's own block. A caller receiving this MUST NOT proceed to
// Phase 3's metered behavioral matrix: the plugin has not yet proven it is built to spec and
// wired, so a paid model run against it would validate nothing real.
type CalibrationBlockedError struct {
	Report *Report
}

func (e *CalibrationBlockedError) Error() string {
	return fmt.Sprintf(
		"plugin-conform: calibration gate blocked for %q -- %d deterministic-conformance error(s) must be fixed before any metered behavioral matrix runs",
		e.Report.PluginName, len(e.Report.Errors),
	)
}

// Calibrate is the mandatory gate every Phase-3 (metered behavioral matrix) caller runs first. It
// returns nil only when report has zero errors, reusing the very same $0 Report Run already
// produced -- calibration is not a second check, it is this phase's own pass/fail read by name,
// positioned as the cheap block in front of the expensive matrix. A nil report is a caller
// defect, reported as a plain error rather than treated as a pass.
func Calibrate(report *Report) error {
	if report == nil {
		return fmt.Errorf("plugin-conform: calibrate: report is nil")
	}
	if !report.Passed() {
		return &CalibrationBlockedError{Report: report}
	}
	return nil
}
