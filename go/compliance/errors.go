package compliance

import "fmt"

// PackagingDefectError wraps a failure to load or parse the invariant registry: missing, empty,
// or corrupt data. This is a packaging defect, never a verdict -- a caller MUST NOT read it as
// "no rung-4 entries" or as a silent pass in either direction.
type PackagingDefectError struct {
	Err error
}

// Error renders the wrapped failure with this package's own prefix.
func (e *PackagingDefectError) Error() string {
	return fmt.Sprintf("compliance: invariant registry packaging defect: %v", e.Err)
}

// Unwrap exposes the underlying read or parse error for errors.Is/As.
func (e *PackagingDefectError) Unwrap() error { return e.Err }
