// Package adoption measures Phase-A forced-use adoption: for each capability this codebase has
// moved behind a governed CLI, did an agent transcript actually route through that CLI, or did
// it reach the same effect through the raw tool call the CLI exists to replace. Building the
// capability was never the failure mode; an agent quietly falling back to the raw tool it
// already knows is - so this package measures routing on owned transcripts, deterministically,
// rather than trusting that a CLI's existence implies its use.
//
// The package is one shared harness over three sibling libraries, not a fourth reimplementation
// of any of them: transcript locates and streams the session logs, gate supplies the
// floor/ceiling verdict primitive Rate measures adoption against, and clikit renders the
// combined Report as the one normalized result record a CLI or CI job emits. Classify and
// CheckFloor are pure functions of their inputs, so a frozen fixture set of transcripts and
// hook-eval records reproduces the same Report on every run.
//
// CheckFloor enforces the one invariant Rate can never trade off against a passing adoption
// number: a governed operation's routing hook must deny-and-redirect a raw invocation, never
// deny the tool's own existence. A gate miss is a rate problem to close over time; a floor
// violation is a defect that governs regardless of how high adoption otherwise measures.
package adoption
