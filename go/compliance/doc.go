// Package compliance wires the invariant registry's rung-4 advisories into enforcement. It
// measures every shipped rung-4 entry's declared per-model compliance floor from a behavioral
// tier's already-run mechanism-check results (LoadResults, MeasureRegistry), writes the measured
// rate and status back onto the registry (Document.Save), and hands a below-floor rate or a
// still-unmeasured entry off to release-transaction's pause hook: a release-pause-register entry
// (PauseRegister) plus a feedback-register defect (FeedbackRegister) naming the owning plugin or
// CLI.
//
// A floor's VALUE is set by this package's own first calibration run, never declared ahead of
// it: MeasureRegistry sets an unset (null) floor to the first measured rate rather than comparing
// against one. A below-floor rate is never retroactive -- ApplyBelowFloor pauses only the owner's
// NEXT release, through the pause register release-transaction's gate reads, and never reopens or
// fails the milestone that originally shipped the invariant. An entry still declared-unmeasured
// at its owner's release is not counted as enforcement: ApplyUnmeasured surfaces it as its own
// defect, but the pause-register row it writes is never the "open" status the gate acts on.
//
// This package never runs a probe itself. It is the wiring between a mechanism check's output
// (LoadResults' input document) and the registry, the pause register, and the feedback register --
// a measurement can only reach any of the three by naming a named, repeatable mechanism over at
// least MinTrials trials, so no single-shot probe path exists.
package compliance
