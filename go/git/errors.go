package git

import (
	"fmt"
	"strings"
)

// CommandError wraps a non-zero git exit, carrying the command's stderr —
// the diagnostic a caller actually wants — separately from the generic
// wrapped error a spawn failure (git not found, bad working directory)
// produces.
type CommandError struct {
	Args     []string
	ExitCode int
	Stderr   string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("git: %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, e.Stderr)
}

// StaleRefError reports a compare-and-swap ref move refused because Ref no
// longer points at ExpectedOld — something moved it between the caller's
// read and this write. The caller must re-read and retry; this package
// never forces through the mismatch.
type StaleRefError struct {
	Ref         string
	ExpectedOld string
	ActualOld   string
}

func (e *StaleRefError) Error() string {
	return fmt.Sprintf("git: ref %s moved: expected %s, found %s — refusing the compare-and-swap", e.Ref, e.ExpectedOld, e.ActualOld)
}

// ConflictError reports a merge or rebase that stopped on unmerged paths.
// This package always aborts back to the pre-operation state before
// returning ConflictError, so Files documents what would have needed manual
// resolution, not a repository state the caller now has to clean up.
type ConflictError struct {
	Op    string // "merge" or "rebase"
	Files []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("git: %s conflict in %d file(s): %s", e.Op, len(e.Files), strings.Join(e.Files, ", "))
}
