package gate

import "fmt"

// PackagingDefectError wraps a failure to load or parse the embedded invariant registry:
// missing, empty, or corrupt data. This is a build defect, never a verdict — a caller MUST
// NOT treat it as "no rung-2 entries" or as a silent pass in either direction.
type PackagingDefectError struct {
	Err error
}

func (e *PackagingDefectError) Error() string {
	return fmt.Sprintf("gate: invariant registry packaging defect: %v", e.Err)
}

// Unwrap exposes the underlying parse or read error for errors.Is/As.
func (e *PackagingDefectError) Unwrap() error {
	return e.Err
}
