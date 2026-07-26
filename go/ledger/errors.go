package ledger

import "fmt"

// ValidationError is returned when a caller-supplied value is malformed: a statement or score
// on Add, a resolution or citation on Resolve, a refuting-evidence/superseded-id relation on
// Retract, or a cycle on Recur. Nothing is written to disk and the ledger's in-memory state is
// left untouched when this is returned.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("ledger: invalid %s: %s", e.Field, e.Message)
}

// NotFoundError is returned by Resolve, Retract, and Recur when id names no entry in the
// ledger.
type NotFoundError struct {
	ID string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("ledger: no entry with id %q", e.ID)
}

// SchemaError wraps a canonical JSON file whose declared schema this package cannot read:
// missing, corrupt, or a version other than SchemaVersion. It is a packaging/state defect,
// never "empty ledger" — a caller must not treat it as zero entries.
type SchemaError struct {
	Path string
	Err  error
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("ledger: %s: %v", e.Path, e.Err)
}

func (e *SchemaError) Unwrap() error {
	return e.Err
}
