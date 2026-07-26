package fsx

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// FindCruft walks root and returns the full paths of every entry ClassifyPath
// resolves to cruftClass under the given, caller-supplied rules. Classification
// runs against each entry's path relative to root, so a pattern like
// "**/*.tmp" matches regardless of where root lives on disk.
//
// A rule with a malformed pattern is never dropped (see ClassifyPath): it fails
// closed, so a broken ruleset over-reports cruft rather than silently missing it.
func FindCruft(root string, rules []Rule, cruftClass string) ([]string, error) {
	var cruft []string
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
		if ClassifyPath(rel, rules).Class == cruftClass {
			cruft = append(cruft, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fsx: find cruft under %s: %w", root, err)
	}
	return cruft, nil
}
