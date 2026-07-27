// Package agentcontract lints agent briefs (the "§Agent instruction & return contract" in the
// toolbelt design) for the properties a machine can actually verify.
//
// An agent brief is a Markdown file with YAML frontmatter, one file per directory literally
// named "agents" (the convention every plugin in this workspace already uses for Claude Code
// subagents). Every brief file directly inside one such directory forms that directory's
// roster — the sibling set is that directory listing, always, never a set an author names in
// frontmatter.
//
// This package checks two independent things:
//
//   - The discriminator matrix (CheckMatrix): for a roster of N briefs, every ordered pair
//     must carry a specific discriminator or an explicit not-confusable declaration with a
//     reason, and a fuzzy boundary must additionally carry a named tie-break. The sibling set
//     a brief is checked against is derived mechanically from the roster directory listing, so
//     an author-nominated subset can never satisfy completeness.
//   - The mechanically-checkable instruction properties (CheckProperties): the output contract is
//     a resolvable schema path rather than prose describing one, every declared failure path
//     names a terminating action, no decision is restated instead of referenced, and the two
//     named defect classes (FB3 — a large artifact must be fragment-written to disk rather than
//     split across dispatches; FB11 — an edit-proposing agent must name the other locations
//     asserting the same claim, with an explicit none) each hold in both the brief's prose and
//     its referenced output schema.
//
// What this package does NOT check is the quality of a cell's content: a discriminator reading
// "different scope" satisfies completeness. That limit is not a bug to work around — it is
// reported by name in every Report so a green run is never mistaken for a quality verdict. See
// Report.ReviewerChecked.
package agentcontract
