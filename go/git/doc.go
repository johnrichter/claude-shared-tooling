// Package git wraps the git operations that are easy to get destructively
// wrong when scripted: rewriting a branch's history and moving where a ref
// points. It shells out to the real git binary for every mutation (git's
// own object model and merge/rebase machinery are the trusted, battle-tested
// implementation; this package never re-derives them) and adds three
// invariants on top that a raw `git` invocation does not give you for free:
//
//   - Resign rewrites a branch's commits to a signed equivalent using
//     commit-tree, reusing each original commit's exact tree. Because no
//     content is re-derived, a resigned commit cannot conflict with anything
//     already merged from its predecessor — the property a rebase (which
//     replays patches) cannot offer over merge history. Resign never shells
//     to `git rebase --exec ... -S`.
//   - Every ref move this package performs — landing a rewrite, deleting a
//     branch — is a compare-and-swap: it fails closed (StaleRefError) if the
//     ref no longer holds the caller's expected value, and it tags the old
//     value for recovery before ever writing the new one. Nothing in this
//     package pushes to a remote; a caller either applies the rewrite
//     locally or receives the exact force-with-lease command to run
//     themselves (SyncMode).
//   - Merge and Rebase always leave the repository clean: a conflict aborts
//     the in-progress merge/rebase before returning ConflictError, so a
//     caller never has to notice and clean up a half-finished operation.
//
// Worktree, Branch, Merge (including octopus), and Rebase round out the
// surface as thin, structured wrappers over their git subcommands.
package git
