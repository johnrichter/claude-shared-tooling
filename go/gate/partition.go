package gate

// Partition splits items into two groups by comparing rank(item) to threshold: everything
// ranking below threshold goes to below, everything ranking at or above it goes to atOrAbove.
// threshold is always caller supplied.
//
// The split is total, lossless, and exactly-once by construction: a single pass over items,
// each item's rank computed once, and each item appended to exactly one of the two returned
// slices — there is no third path out and no way for len(below)+len(atOrAbove) to differ
// from len(items).
func Partition[T any](items []T, threshold int, rank func(T) int) (below, atOrAbove []T) {
	for _, item := range items {
		if rank(item) < threshold {
			below = append(below, item)
		} else {
			atOrAbove = append(atOrAbove, item)
		}
	}
	return below, atOrAbove
}
