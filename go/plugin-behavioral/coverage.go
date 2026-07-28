package plugin_behavioral

import "sort"

// MissingCoverage returns manifestCaseIDs minus executedCaseIDs -- the set difference a coverage
// report needs, per schemas/plugin-validation's own contract: a manifest stores both sides of the
// subtraction, and whichever consumer needs the result computes it. Sorted and deduplicated for a
// stable, readable report. Behavioral coverage is an accounting figure, never a build-acceptance
// bar -- a non-empty result names a gap to close, not a failure this package itself raises.
func MissingCoverage(manifestCaseIDs, executedCaseIDs []string) []string {
	executed := make(map[string]bool, len(executedCaseIDs))
	for _, id := range executedCaseIDs {
		executed[id] = true
	}
	seen := make(map[string]bool, len(manifestCaseIDs))
	var missing []string
	for _, id := range manifestCaseIDs {
		if executed[id] || seen[id] {
			continue
		}
		seen[id] = true
		missing = append(missing, id)
	}
	sort.Strings(missing)
	return missing
}
