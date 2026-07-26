package fsx

import (
	"fmt"
	"os"
)

// Move relocates src to dst via a single rename syscall: the data never exists
// in two places at once, and there is no read-copy-delete window where a crash
// could leave dst partially written or drop the file entirely.
//
// Rename cannot cross filesystem/device boundaries; Move deliberately does not
// fall back to a non-atomic copy+delete in that case (a fallback would defeat the
// atomicity guarantee this function exists to provide) and instead returns the
// underlying error. Callers that need a cross-device relocation must implement
// their own copy-then-verify-then-delete and accept its weaker guarantees.
func Move(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("fsx: move %s -> %s: %w", src, dst, err)
	}
	return nil
}
