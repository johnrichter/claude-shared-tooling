package fsx

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// Enumerator describes one place in the codebase that claims to account for a
// set of files under a directory (a build manifest, a glob in a script, a list
// baked into a tool) by the doublestar glob patterns it uses to find them.
type Enumerator struct {
	Name     string
	Patterns []string
}

// Drift is a file under the scanned root that no enumerator's patterns cover —
// evidence that a new file class was introduced without updating whatever is
// supposed to track it.
type Drift struct {
	Path string // full path of the unaccounted-for file
}

// ScanEnumerators walks root and reports every regular file not matched by any
// pattern of any given enumerator. Matching runs against each file's path
// relative to root.
//
// A malformed enumerator pattern is treated as matching nothing, never as
// matching everything: an enumerator can only vouch for a file by actually
// matching it, so a broken pattern surfaces its files as drift instead of
// silently masking them.
func ScanEnumerators(root string, enumerators []Enumerator) ([]Drift, error) {
	var drift []Drift
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if !anyEnumeratorMatches(relSlash, enumerators) {
			drift = append(drift, Drift{Path: path})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fsx: scan enumerators under %s: %w", root, err)
	}
	return drift, nil
}

func anyEnumeratorMatches(relSlash string, enumerators []Enumerator) bool {
	for _, e := range enumerators {
		for _, pattern := range e.Patterns {
			if matched, err := doublestar.Match(pattern, relSlash); err == nil && matched {
				return true
			}
		}
	}
	return false
}
