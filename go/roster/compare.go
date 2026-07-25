package roster

import "fmt"

// Compare orders two model IDs by capability. Same family: generation compares element-wise,
// first differing element decides, and a prefix ranks below its extension ([5] outranks [4,8];
// [5,1] outranks [5]). Different families: cross_family_rank compares directly and must be
// declared on both sides — an undeclared pair is roster-stale, never a default ordering.
// Returns -1 if a ranks below b, 0 if equal, 1 if a ranks above b.
func Compare(a, b string) (int, error) {
	ma, err := Lookup(a)
	if err != nil {
		return 0, err
	}
	mb, err := Lookup(b)
	if err != nil {
		return 0, err
	}
	if ma.Family == mb.Family {
		return compareGenerations(ma.Generation, mb.Generation), nil
	}
	if ma.CrossFamilyRank == nil || mb.CrossFamilyRank == nil {
		return 0, &StaleError{
			Query:  fmt.Sprintf("%s vs %s", ma.ID, mb.ID),
			Reason: fmt.Sprintf("no declared cross_family_rank between %q and %q", ma.ID, mb.ID),
		}
	}
	return compareInts(*ma.CrossFamilyRank, *mb.CrossFamilyRank), nil
}

func compareGenerations(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return compareInts(a[i], b[i])
		}
	}
	return compareInts(len(a), len(b))
}

func compareInts(x, y int) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	default:
		return 0
	}
}
