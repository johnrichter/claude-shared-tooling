package jsondoc

import (
	"fmt"
	"sort"
)

// Entry is one member of a keyed set of JSON documents, identified by its
// key and the content hash of its document.
type Entry struct {
	Key  string
	Hash string
}

// Change is a key present on both sides of a Diff whose document's content
// hash differs between before and after.
type Change struct {
	Key     string
	OldHash string
	NewHash string
}

// DocDiff classifies every key across two keyed sets of JSON documents —
// a dependency set, a task plan, or any other collection addressed by a
// stable key — into exactly one of four buckets. Each slice is sorted by
// key for deterministic output.
type DocDiff struct {
	Carried []Entry  // same key, same content hash on both sides
	Changed []Change // same key, different content hash
	Added   []Entry  // key present only in after
	Removed []Entry  // key present only in before
}

// Diff compares two keyed sets of JSON documents, before and after, and
// returns a DocDiff. A key is Carried when its document's content hash is
// identical on both sides, Changed when the key exists on both sides but
// the hash differs, Added when the key only exists in after, and Removed
// when the key only exists in before. A nil or empty map is treated as an
// empty set on that side, so a wholly-missing before (a first-ever run) or
// after (a full removal) is handled without special-casing.
//
// Each document is hashed with ContentHash, which canonicalizes it first:
// key order, whitespace, and float formatting never register as a change.
// A document is any value ContentHash accepts, including raw JSON
// ([]byte / json.RawMessage). A document whose JSON repeats an object key
// at the same level is rejected — Diff returns an error naming the
// offending key rather than resolving the diff against an ambiguous value.
func Diff(before, after map[string]any) (DocDiff, error) {
	beforeHash := make(map[string]string, len(before))
	for key, doc := range before {
		hash, err := ContentHash(doc)
		if err != nil {
			return DocDiff{}, fmt.Errorf("jsondoc: diff: before[%q]: %w", key, err)
		}
		beforeHash[key] = hash
	}

	var diff DocDiff
	seen := make(map[string]bool, len(after))
	for key, doc := range after {
		seen[key] = true
		hash, err := ContentHash(doc)
		if err != nil {
			return DocDiff{}, fmt.Errorf("jsondoc: diff: after[%q]: %w", key, err)
		}
		old, existed := beforeHash[key]
		switch {
		case !existed:
			diff.Added = append(diff.Added, Entry{Key: key, Hash: hash})
		case old == hash:
			diff.Carried = append(diff.Carried, Entry{Key: key, Hash: hash})
		default:
			diff.Changed = append(diff.Changed, Change{Key: key, OldHash: old, NewHash: hash})
		}
	}
	for key, hash := range beforeHash {
		if !seen[key] {
			diff.Removed = append(diff.Removed, Entry{Key: key, Hash: hash})
		}
	}

	sort.Slice(diff.Carried, func(i, j int) bool { return diff.Carried[i].Key < diff.Carried[j].Key })
	sort.Slice(diff.Changed, func(i, j int) bool { return diff.Changed[i].Key < diff.Changed[j].Key })
	sort.Slice(diff.Added, func(i, j int) bool { return diff.Added[i].Key < diff.Added[j].Key })
	sort.Slice(diff.Removed, func(i, j int) bool { return diff.Removed[i].Key < diff.Removed[j].Key })
	return diff, nil
}
