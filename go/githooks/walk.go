package githooks

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// SkipClass is the fsx.Rule class a caller uses to mark a path skipped by
// every content scan in this package - VCS internals, build/venv artifacts,
// vendored trees. Any other class (or no match) means "scan it".
const SkipClass = "skip"

// walkScannable walks root and calls visit(relPath, absPath) for every
// regular file fsx.ClassifyPath does not resolve to skipRules' SkipClass.
// Matching runs against each file's slash-separated path relative to root, so
// a rule like "**/.git/**" skips the directory wherever it sits under root.
func walkScannable(root string, skipRules []fsx.Rule, visit func(rel, abs string) error) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if fsx.ClassifyPath(rel, skipRules).Class == SkipClass {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		return visit(filepath.ToSlash(rel), path)
	})
	if err != nil {
		return fmt.Errorf("githooks: walk %s: %w", root, err)
	}
	return nil
}
