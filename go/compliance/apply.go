package compliance

import (
	"fmt"
	"time"
)

// belowFloorKey and unmeasuredKey are the FeedbackEntry.SourceTaskID markers Ensure de-duplicates
// on, keyed so a below-floor miss and an unmeasured advisory for the same invariant never collide.
func belowFloorKey(invariantID, model string) string {
	return "rung4-below-floor:" + invariantID + ":" + model
}
func unmeasuredKey(invariantID string) string { return "rung4-unmeasured:" + invariantID }

// ApplyBelowFloor folds every below-floor finding into pause and fb: each opens (or reuses) a
// feedback-register defect naming the owner and invariant, and upserts the matching release-
// pause-register entry release-transaction's gate reads as an open pause on the owner's NEXT
// release. A repeat measurement of the same miss refreshes both rows rather than duplicating
// them; it never touches the invariant's own registry entry, so the milestone that shipped the
// invariant is never revisited by this call.
func ApplyBelowFloor(pause PauseRegister, fb FeedbackRegister, findings []BelowFloor, ownerKind func(owner string) string, at time.Time) (PauseRegister, FeedbackRegister) {
	stamp := at.UTC().Format(time.RFC3339)
	for _, f := range findings {
		pause, fb = applyOneBelowFloor(pause, fb, f, ownerKind(f.Owner), stamp, at)
	}
	return pause, fb
}

func applyOneBelowFloor(pause PauseRegister, fb FeedbackRegister, f BelowFloor, kind, stamp string, at time.Time) (PauseRegister, FeedbackRegister) {
	fb, entry := fb.Ensure(belowFloorKey(f.InvariantID, f.Model), func(id string) FeedbackEntry {
		return FeedbackEntry{
			ID:    id,
			Title: fmt.Sprintf("compliance-floor-miss: %s below floor for %s on %s", f.InvariantID, f.Owner, f.Model),
			Feedback: fmt.Sprintf(
				"compliance measurement found %s's invariant %q measured %.2f on %s, below its declared floor %.2f (mechanism: %s).",
				f.Owner, f.InvariantID, f.MeasuredRate, f.Model, f.DeclaredFloor, f.Mechanism,
			),
			ProposedSolution: fmt.Sprintf("Fix %s's honoring of %s on %s; once a re-measurement clears the floor, resolve this pause-register entry to unblock the owner's releases -- it stays open, and keeps pausing, until it is resolved.", f.Owner, f.InvariantID, f.Model),
			WhyItMatters:     "an open below-floor defect pauses this owner's NEXT release; it never retroactively fails the milestone that shipped the invariant.",
			Impact:           belowFloorImpact,
			Urgency:          belowFloorUrgency,
			Added:            stamp,
		}
	})
	pause = pause.UpsertOpen(f, kind, entry.ID, at)
	return pause, fb
}

// ApplyUnmeasured folds every still-unmeasured entry into pause and fb: a declared-unmeasured row
// is visible in both registers but carries no "open" status, so release-transaction's gate never
// pauses a release for it -- the schema-completeness limit rung 4 already declares (unmeasured is
// not enforcement). A repeat check for the same invariant opens no second defect.
func ApplyUnmeasured(pause PauseRegister, fb FeedbackRegister, entries []*Entry, ownerKind func(owner string) string, at time.Time) (PauseRegister, FeedbackRegister) {
	stamp := at.UTC().Format(time.RFC3339)
	for _, e := range entries {
		pause, fb = applyOneUnmeasured(pause, fb, e, ownerKind(e.Owner), stamp)
	}
	return pause, fb
}

func applyOneUnmeasured(pause PauseRegister, fb FeedbackRegister, e *Entry, kind, stamp string) (PauseRegister, FeedbackRegister) {
	fb, entry := fb.Ensure(unmeasuredKey(e.ID), func(id string) FeedbackEntry {
		return FeedbackEntry{
			ID:    id,
			Title: fmt.Sprintf("compliance-unmeasured-at-release: %s (%s) still declared-unmeasured", e.ID, e.Owner),
			Feedback: fmt.Sprintf(
				"%s's rung-4 invariant %q is still declared-unmeasured at a release; an advisory unmeasured at its owner's release is not counted as enforcement.",
				e.Owner, e.ID,
			),
			ProposedSolution: fmt.Sprintf("Run the behavioral tier's measurement against %s and re-measure before the next release.", e.ID),
			WhyItMatters:     "a rung-4 floor that is never measured never becomes a control -- this is the gap the compliance-floor measurement wiring exists to close.",
			Impact:           unmeasuredImpact,
			Urgency:          unmeasuredUrgency,
			Added:            stamp,
		}
	})
	pause = pause.UpsertUnmeasured(e.ID, e.Owner, kind, entry.ID)
	return pause, fb
}
