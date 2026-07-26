package clikit

import "fmt"

// Result is one CLI invocation's outcome in its normalized form: the single
// record a clikit CLI writes to stdout, whatever the outcome. It is a pure
// function of the invocation - no clock, no host, no build version - so
// identical input yields identical bytes.
type Result struct {
	SchemaVersion int            `json:"schema_version"`
	Command       []string       `json:"command"`
	Status        Status         `json:"status"`
	ExitCode      int            `json:"exit_code"`
	Data          map[string]any `json:"data,omitempty"`
	Errors        []Diagnostic   `json:"errors,omitempty"`
	Caveats       []Diagnostic   `json:"caveats,omitempty"`
}

// NewSuccess builds a class-0 result: the command did what was asked and the
// result is complete and unqualified.
func NewSuccess(command []string, data map[string]any) (*Result, error) {
	return newResult(StatusSuccess, command, data, nil, nil)
}

// NewCaveats builds a class-10 result: the command did what was asked but
// the result is qualified. At least one caveat is required.
func NewCaveats(command []string, data map[string]any, caveats []Diagnostic) (*Result, error) {
	if len(caveats) == 0 {
		return nil, fmt.Errorf("clikit: caveats status requires at least one caveat")
	}
	return newResult(StatusCaveats, command, data, nil, caveats)
}

// NewGateNegative builds a class-20 result: an expected negative - a
// question was asked about something that exists, and the answer is no.
// errors[0] must be the governing finding.
func NewGateNegative(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusGateNegative, command, data, errors, caveats)
}

// NewPreconditionUnmet builds a class-30 result: the state the operation
// requires is not in place, and the operation was not attempted.
func NewPreconditionUnmet(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusPreconditionUnmet, command, data, errors, caveats)
}

// NewNotFound builds a class-40 result: a subject the caller named does not
// exist.
func NewNotFound(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusNotFound, command, data, errors, caveats)
}

// NewConflict builds a class-41 result: the subject exists in a state
// incompatible with the request.
func NewConflict(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusConflict, command, data, errors, caveats)
}

// NewUsage builds a class-50 result: the invocation itself is wrong and
// nothing was attempted.
func NewUsage(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusUsage, command, data, errors, caveats)
}

// NewTransient builds a class-60 result: an identical re-invocation may
// resolve it, with no change by anyone.
func NewTransient(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusTransient, command, data, errors, caveats)
}

// NewPermission builds a class-70 result: access is refused, and an
// identical re-invocation will be refused again.
func NewPermission(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusPermission, command, data, errors, caveats)
}

// NewUnsupported builds a class-80 result: a well-formed request this tool
// does not serve, by scope, platform or version.
func NewUnsupported(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusUnsupported, command, data, errors, caveats)
}

// NewInternal builds a class-90 result: the tool itself failed, or produced
// an outcome it cannot classify.
func NewInternal(command []string, data map[string]any, errors []Diagnostic, caveats []Diagnostic) (*Result, error) {
	return newResult(StatusInternal, command, data, errors, caveats)
}

func newResult(status Status, command []string, data map[string]any, errors, caveats []Diagnostic) (*Result, error) {
	r := &Result{
		SchemaVersion: SchemaVersion,
		Command:       command,
		Status:        status,
		ExitCode:      status.ExitCode(),
		Data:          data,
		Errors:        errors,
		Caveats:       caveats,
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// Governing returns the record's governing error - errors[0] - and true when
// one is present. Its code's class always equals r.Status, which is what
// makes the exit code derivable from the record.
func (r *Result) Governing() (Diagnostic, bool) {
	if len(r.Errors) == 0 {
		return Diagnostic{}, false
	}
	return r.Errors[0], true
}

// Validate checks r against the rules result-record.schema.json pins: the
// status/exit_code pairing, which of errors/caveats each class requires or
// forbids, every diagnostic's shape and class, and the record's bounds. It
// is the last gate before a record is canonicalized and emitted.
func (r *Result) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("clikit: schema_version must be %d, got %d", SchemaVersion, r.SchemaVersion)
	}
	if !r.Status.Known() {
		return fmt.Errorf("clikit: unknown status %q", string(r.Status))
	}
	if r.ExitCode != r.Status.ExitCode() {
		return fmt.Errorf("clikit: exit_code %d does not pair with status %q (want %d)", r.ExitCode, r.Status, r.Status.ExitCode())
	}
	if len(r.Command) == 0 || len(r.Command) > 8 {
		return fmt.Errorf("clikit: command has %d elements, want 1-8", len(r.Command))
	}
	if !isToolName(r.Command[0]) {
		return fmt.Errorf("clikit: invalid command[0] %q", r.Command[0])
	}
	for _, c := range r.Command[1:] {
		if !isSubcommandName(c) {
			return fmt.Errorf("clikit: invalid command element %q", c)
		}
	}
	if r.Data != nil {
		if len(r.Data) == 0 || len(r.Data) > 64 {
			return fmt.Errorf("clikit: data has %d members, want 1-64", len(r.Data))
		}
		for k := range r.Data {
			if !isDataKey(k) {
				return fmt.Errorf("clikit: invalid data key %q", k)
			}
		}
	}
	if err := r.validatePairing(); err != nil {
		return err
	}
	if len(r.Errors) > 50 {
		return fmt.Errorf("clikit: errors has %d members, max 50", len(r.Errors))
	}
	for i, e := range r.Errors {
		if err := e.validateAsError(); err != nil {
			return fmt.Errorf("clikit: errors[%d]: %w", i, err)
		}
	}
	if len(r.Caveats) > 50 {
		return fmt.Errorf("clikit: caveats has %d members, max 50", len(r.Caveats))
	}
	for i, c := range r.Caveats {
		if err := c.validateAsCaveat(); err != nil {
			return fmt.Errorf("clikit: caveats[%d]: %w", i, err)
		}
	}
	return nil
}

// validatePairing enforces the schema's per-status branch: which of errors
// and caveats is required, forbidden or optional, and that a failure
// class's governing error carries that class's code prefix.
func (r *Result) validatePairing() error {
	switch r.Status {
	case StatusSuccess:
		if len(r.Errors) != 0 || len(r.Caveats) != 0 {
			return fmt.Errorf("clikit: success forbids errors and caveats")
		}
	case StatusCaveats:
		if len(r.Errors) != 0 {
			return fmt.Errorf("clikit: caveats status forbids errors")
		}
		if len(r.Caveats) == 0 {
			return fmt.Errorf("clikit: caveats status requires at least one caveat")
		}
	default:
		if len(r.Errors) == 0 {
			return fmt.Errorf("clikit: %s requires at least one error", r.Status)
		}
		class, ok := diagnosticClass(r.Errors[0].Code)
		if !ok || class != string(r.Status) {
			return fmt.Errorf("clikit: governing error %q is not in the %s class", r.Errors[0].Code, r.Status)
		}
	}
	return nil
}
