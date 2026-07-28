package compliance

// UnmeasuredAtRelease reports every shipped rung-4 entry owned by owner whose measurement_status
// is still StatusDeclaredUnmeasured -- the invariants a release-time check must ask about before
// treating owner's rung-4 advisories as controls. An advisory unmeasured at its owner's release is
// not counted as enforcement: it is surfaced (see ApplyUnmeasured), never treated as a pass.
func UnmeasuredAtRelease(doc *Document, owner string) []*Entry {
	var out []*Entry
	for _, e := range doc.RungFourShipped() {
		if e.Owner == owner && e.MeasurementStatus != StatusMeasured {
			out = append(out, e)
		}
	}
	return out
}
