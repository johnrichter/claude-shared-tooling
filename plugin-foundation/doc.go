// Package plugin_foundation is the forced-use plugin foundation every govern-now CLI's plugin
// reuses instead of hand-authoring its own: a data-driven routing-rules format naming each
// governed operation's CLI invocation and the raw tool call it supersedes, plus BuildRegistry,
// the one function that turns a plugin's routing-rules.json into the adoption library's
// []GovernedOperation. A per-CLI plugin authors data (routing-rules.json); it never writes its
// own Classify closures, so the match semantics — a Bash command prefix, or a bare tool name for
// a non-Bash operation — live in exactly one place across every plugin instead of N copies.
//
// The same routing-rules.json also drives forced-use-hook.sh, the fail-open PreToolUse hook every
// plugin installs verbatim: the hook and BuildRegistry read identical prefix-match semantics from
// the same file, so a plugin's live routing decision and its adoption measurement can never drift
// apart. download-script.sh is the sibling provisioning half: it resolves the plugin's pinned
// version, fetches the matching per-OS/arch binary, verifies its checksum, and caches it
// idempotently, exporting the verified path the hook checks before it ever considers denying a
// raw invocation.
package plugin_foundation
