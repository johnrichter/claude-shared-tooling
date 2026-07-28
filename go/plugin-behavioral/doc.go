// Package plugin_behavioral runs plugin-validation's Phase 3, the metered behavioral matrix: the
// only tier that proves a plugin actually changes Claude's observed behavior, not merely that it
// loads or is wired to spec (Phases 1 and 2's job).
//
// Run drives an N-trials x models matrix over a caller-supplied Case set. A KindProbe case is a
// single-shot, live `claude -p` invocation this package captures and grades itself. A KindAgentic
// case is graded WITHOUT ever launching a probe: a $0 MechanismCheck plus an Observer over a real,
// already-captured multi-turn session transcript (main plus any subagent fan-out). This split
// exists because a single-shot probe cannot validate an agentic/multi-step behavior (dispatch,
// orchestration) -- a tiny prompt in an empty session gives a model no reason to dispatch, so an
// agentic case is only ever gradeable against a session that actually ran multi-turn.
//
// Every generated case passes through the same guards before it can count as a pass: LintPrompt
// (leakage lint) rejects a probe case whose own prompt names what it tests, and GradeInconclusive
// biases a positive case's inconclusive verdict to Violated rather than a silent, vacuous pass.
// Before any of that, Run enforces, in cheapest-first order: the calibration gate already
// computed by Phase 2 (Options.Calibration), the live-run safety interlock (RequireLiveOptIn), the
// environment-fidelity assertion (AssertPluginActive) and the pinned Claude Code version
// (AssertCCVersionPinned) -- every one fails loud rather than silently grading nothing.
//
// Budget is enforced twice, independently: PerTrialBudgetUSD bounds one trial's own real spend
// (checkTrialBudget aborts that trial the moment it's crossed, regardless of whether the probe
// itself honored --max-budget-usd), and the matrix's cumulative spend is bounded by a hard total
// ceiling (TotalBudgetUSD, or trials x models x PerTrialBudgetUSD when left at zero) that stops the
// whole run early. Report.MissingCoverage is manifest-minus-executed case ids -- an accounting
// figure this package computes, never a build-acceptance bar: a behavioral "honored" verdict is
// probabilistic by nature and is graded fail-safe (biased toward not-honored) throughout.
//
// This package carries forward, as its own properties rather than rediscovering them: the
// live-run safety interlock (RequireLiveOptIn), the marker-carrying throwaway working directory
// every KindProbe case runs in (Provision/AssertThrowaway), the per-trial and per-run cost
// ceilings above, the transcript classifier kept separate from the invocation harness (classify.go
// and archetypes.go import neither sysops nor a subprocess seam; capture.go is the one file that
// does), and the ambient model-pin override (ModelPinEnv/ResolveModel).
package plugin_behavioral
