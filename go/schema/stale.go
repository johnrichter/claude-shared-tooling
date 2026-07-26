package schema

import (
	"fmt"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// CheckStale inspects a document's declared created/updated timestamps and returns normalized
// findings. A nil created or updated means the document does not declare that field and is
// skipped for the check that needs it.
//
// Two findings are possible, and both can fire on the same call:
//   - a "caveats.stale_updated" caveat when updated is older than maxAge relative to now;
//   - a "usage.date_contradiction" error when created is after updated — a date pairing that
//     cannot have happened.
//
// The returned error is non-nil only when a diagnostic itself could not be built.
func CheckStale(created, updated *time.Time, now time.Time, maxAge time.Duration) ([]clikit.Diagnostic, error) {
	var diags []clikit.Diagnostic

	if created != nil && updated != nil && created.After(*updated) {
		message := fmt.Sprintf("created %s is after updated %s", created.Format(time.RFC3339), updated.Format(time.RFC3339))
		triage := clikit.Manual("correct the document's created/updated dates so created is not after updated")
		d, err := clikit.NewError("usage.date_contradiction", message, triage, map[string]any{
			"created": created.Format(time.RFC3339),
			"updated": updated.Format(time.RFC3339),
		})
		if err != nil {
			return nil, fmt.Errorf("schema: normalize date contradiction: %w", err)
		}
		diags = append(diags, d)
	}

	if updated != nil {
		if age := now.Sub(*updated); age > maxAge {
			message := fmt.Sprintf("updated %s is older than the %s staleness bound", updated.Format(time.RFC3339), maxAge)
			triage := clikit.Manual("review the document and refresh its updated date, or confirm it is still current")
			d, err := clikit.NewCaveat("caveats.stale_updated", message, triage, map[string]any{
				"updated":     updated.Format(time.RFC3339),
				"age_seconds": int64(age.Seconds()),
			})
			if err != nil {
				return nil, fmt.Errorf("schema: normalize staleness: %w", err)
			}
			diags = append(diags, d)
		}
	}

	return diags, nil
}
