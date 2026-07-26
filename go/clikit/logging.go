package clikit

import (
	"github.com/johnrichter/claude-shared-tooling/go/logkit"
)

// clikit does not define logging: every log line a clikit CLI writes to
// stderr is a logkit record, built and emitted through logger. This file is
// the one seam clikit owns - the mapping from a clikit diagnostic onto a
// logkit record - and touches no logkit internals beyond its public Logger.

// rootFieldNames are logkit's Record field names. A diagnostic context
// member colliding with one of them is nested under the reserved `clikit`
// key instead of being dropped or renamed.
var rootFieldNames = map[string]bool{
	"caller": true, "error": true, "fields": true, "level": true,
	"message": true, "schema_version": true, "service": true,
	"service_version": true, "timestamp": true,
}

// diagnosticError adapts a Diagnostic's message into a Go error so
// logkit.Logger.Error can populate the log record's error.message verbatim,
// per the contract's mapping. Its type name becomes the log record's
// error.kind - independent of, and never overwritten by, the diagnostic's
// own code.
type diagnosticError struct{ message string }

func (e diagnosticError) Error() string { return e.message }

// clikitLogFields builds the reserved `clikit` fields object
// (clikit.contract.json logkit.reserved_log_field) for r's terminating
// record: exit_code and status always, error_code when r has a governing
// diagnostic.
func clikitLogFields(r *Result) map[string]any {
	f := map[string]any{
		"exit_code": r.ExitCode,
		"status":    string(r.Status),
	}
	if d, ok := r.Governing(); ok {
		f["error_code"] = d.Code
	}
	return f
}

// mergeDiagnosticFields folds a diagnostic's context into logkit fields per
// the contract's error_mapping: each context member becomes a same-named
// fields member, except one that collides with a logkit root field name,
// which is nested under fields.clikit instead.
func mergeDiagnosticFields(fields logkit.Fields, clikitField map[string]any, context map[string]any) logkit.Fields {
	if len(context) == 0 {
		return fields
	}
	if fields == nil {
		fields = logkit.Fields{}
	}
	for k, v := range context {
		if rootFieldNames[k] {
			clikitField[k] = v
			continue
		}
		fields[k] = v
	}
	return fields
}

// LogTerminating writes the one logkit record every clikit CLI emits
// immediately before exit: fields.clikit.exit_code and fields.clikit.status
// at r.Status's default level (StatusInternal maps to logkit's `fatal`,
// which itself has no side effect - the process exits through r.ExitCode,
// not through logkit). message overrides the default; pass "" to use the
// governing diagnostic's message, or a generic completion message when there
// is none.
func LogTerminating(logger *logkit.Logger, r *Result, message string) error {
	clikitField := clikitLogFields(r)
	fields := logkit.Fields{"clikit": clikitField}
	var logErr error

	if d, ok := r.Governing(); ok {
		fields = mergeDiagnosticFields(fields, clikitField, d.Context)
		logErr = diagnosticError{message: d.Message}
		if message == "" {
			message = d.Message
		}
	}
	if message == "" {
		message = r.Command[len(r.Command)-1] + " completed"
	}
	return logger.Emit(r.Status.LogLevel(), logErr, message, fields)
}
