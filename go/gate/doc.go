// Package gate is the one place threshold/band decision logic lives, so no second
// implementation of "compare a rank to a range and pick a verdict" accumulates elsewhere in
// this tree. It has two halves:
//
//   - Band and Partition: parameterized floor/ceiling and split-by-threshold primitives.
//     Every bound is a caller-supplied argument — this package never hardcodes one — and an
//     undetectable input (a dimension with no signal to measure) resolves to an explicit,
//     loud warning rather than silently passing the whole band.
//   - The enforcement-invariant-registry consumer: loads the registry
//     (schemas/invariant-registry/invariant-registry.json) and exposes, for every shipped
//     rung-2 entry, the declared trigger/condition/fail-direction a caller compares against a
//     gate's actual firing. Rung-1 and rung-3 entries are not this package's concern — they
//     route to the invariant linter — and rung-4/rung-5 entries have no consumer at all by
//     design; this package only asserts their schema completeness.
//
// A missing or corrupt registry is a packaging defect (PackagingDefectError), never a verdict
// in either direction: a caller that gets one must not treat it as a pass or a fail.
package gate
