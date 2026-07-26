package fsx

import (
	"fmt"
	"io/fs"

	"github.com/google/renameio/v2"
)

// WriteAtomic durably writes data to path: it writes to a temp file in the same
// directory, fsyncs it, then renames it into place. A crash or power loss at any
// point leaves either the old contents or the new contents at path, never a
// partial write.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := renameio.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("fsx: write atomic %s: %w", path, err)
	}
	return nil
}
