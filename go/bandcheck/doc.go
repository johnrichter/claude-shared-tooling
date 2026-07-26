// Package bandcheck checks two things a governed session or a governance rule can silently get
// wrong: whether the session itself is running inside its assigned model/effort tier, and
// whether a rule's declared firing scope matches what actually fires.
//
// Tier: SelfCheck resolves the observed (model, effort) session tier against a caller-supplied
// TierBand and reports the verdict by composing gate.Band — this package holds no second
// floor/ceiling/abort/warn/silent decision. DetectSessionModel is the tier-detection input: it
// resolves a session's live model from a transcript, but only from lines it can attribute to
// the orchestrator (transcript.AuthorOrchestrator) — never a subagent turn, and never a line
// whose authorship is unknown, even if that line happens to be the transcript's last one.
//
// Firing precision: CheckOverfire compares a workspace rule's declared firing scope against
// where it actually fires. A rule's `paths:` frontmatter is its machine-checked trigger; the
// rule body's own **Scope.** paragraph is the rule author's declared intent for that trigger.
// CheckOverfire expands `paths:` against a real tree and flags any match the Scope paragraph
// never named as an overfire — and, critically, treats a missing Scope paragraph as its own
// loud, distinct outcome rather than folding it into a false "fully precise" verdict.
//
// CheckRegistryFiring is the sibling SC-ENFORCE check for rung-2 gate declarations: it reads
// declared triggers from the invariant registry via gate.Rung2Declarations (never a second copy
// of the registry) and compares them against real observed gate firings, failing on either a
// declared trigger that never fired or an actual firing no declared trigger covers.
//
// ProbeFiring is the one mechanism this package uses to establish REAL firing, as opposed to
// static glob precision: it plants an artifact, drives a single `claude -p` run against it
// through an injectable subprocess seam (sysops.Run in production), and observes the outcome by
// reading the run's own session transcript (transcript.TranscriptSource) — never by re-deriving
// firing from the registry or from the glob check.
package bandcheck
