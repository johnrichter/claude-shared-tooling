package ledger

import "fmt"

// ValidationError is returned by Add when a statement or score is malformed. Nothing is
// written to disk and no entry is appended when this is returned.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("ledger: invalid %s: %s", e.Field, e.Message)
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
