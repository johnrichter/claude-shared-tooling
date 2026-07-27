package clikit

import "fmt"

// Diagnostic is one structured, actionable statement of why a result is not
// plain success: an error or a caveat, the two share this shape. `Triage` is
// always required - a diagnostic that does not say what to do next is not
// finished.
type Diagnostic struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Triage  Triage         `json:"triage"`
	Context map[string]any `json:"context,omitempty"`
}

// NewError builds an errors-array member. code's class (its first segment)
// must be one of the nine failure classes - never `caveats`.
func NewError(code, message string, triage Triage, context map[string]any) (Diagnostic, error) {
	class, ok := diagnosticClass(code)
	if !ok {
		return Diagnostic{}, fmt.Errorf("clikit: malformed diagnostic code %q", code)
	}
	if class == string(StatusCaveats) {
		return Diagnostic{}, fmt.Errorf("clikit: error code %q must not be in the caveats class", code)
	}
	return newDiagnostic(code, message, triage, context)
}

// NewCaveat builds a caveats-array member. code's class must be `caveats` -
// an error can never be filed as a caveat.
func NewCaveat(code, message string, triage Triage, context map[string]any) (Diagnostic, error) {
	class, ok := diagnosticClass(code)
	if !ok {
		return Diagnostic{}, fmt.Errorf("clikit: malformed diagnostic code %q", code)
	}
	if class != string(StatusCaveats) {
		return Diagnostic{}, fmt.Errorf("clikit: caveat code %q must be in the caveats class", code)
	}
	return newDiagnostic(code, message, triage, context)
}

func newDiagnostic(code, message string, triage Triage, context map[string]any) (Diagnostic, error) {
	if !isLine(message) {
		return Diagnostic{}, fmt.Errorf("clikit: invalid diagnostic message %q", message)
	}
	if err := triage.validate(); err != nil {
		return Diagnostic{}, err
	}
	if len(context) > 32 {
		return Diagnostic{}, fmt.Errorf("clikit: diagnostic context has %d members, max 32", len(context))
	}
	for k := range context {
		if !isDataKey(k) {
			return Diagnostic{}, fmt.Errorf("clikit: invalid context key %q", k)
		}
	}
	return Diagnostic{Code: code, Message: message, Triage: triage, Context: context}, nil
}

// validateAsError re-checks d as it must appear inside `errors`: well-formed
// and not in the caveats class. Used by Result.Validate on a record built by
// hand rather than through NewError.
func (d Diagnostic) validateAsError() error {
	class, ok := diagnosticClass(d.Code)
	if !ok {
		return fmt.Errorf("clikit: malformed diagnostic code %q", d.Code)
	}
	if class == string(StatusCaveats) {
		return fmt.Errorf("clikit: error code %q must not be in the caveats class", d.Code)
	}
	return d.validateShape()
}

// validateAsCaveat re-checks d as it must appear inside `caveats`.
func (d Diagnostic) validateAsCaveat() error {
	class, ok := diagnosticClass(d.Code)
	if !ok {
		return fmt.Errorf("clikit: malformed diagnostic code %q", d.Code)
	}
	if class != string(StatusCaveats) {
		return fmt.Errorf("clikit: caveat code %q must be in the caveats class", d.Code)
	}
	return d.validateShape()
}

func (d Diagnostic) validateShape() error {
	if !isLine(d.Message) {
		return fmt.Errorf("clikit: invalid diagnostic message %q", d.Message)
	}
	if err := d.Triage.validate(); err != nil {
		return err
	}
	if len(d.Context) > 32 {
		return fmt.Errorf("clikit: diagnostic context has %d members, max 32", len(d.Context))
	}
	for k := range d.Context {
		if !isDataKey(k) {
			return fmt.Errorf("clikit: invalid context key %q", k)
		}
	}
	return nil
}
