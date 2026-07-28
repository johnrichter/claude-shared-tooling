// Package plugin_conform runs plugin-validation's Phase 2, deterministic conformance: the $0,
// no-model half of the harness that proves a Claude Code plugin is built to its own declared
// spec and wired to fire -- never that Claude actually honors it, which is Phase 3's job.
//
// Run reads a plugin's own files under one PluginDir and performs five static checks: its
// manifest and component frontmatter parse (and, when a schema is supplied, validate); its
// hooks.json is well-formed; every rules/*.md paths: glob resolves to at least one real file
// inside the plugin's own tree; and the mechanism tier confirms each hook's matcher actually
// fires on what it declares and stays silent otherwise, every launcher the plugin depends on
// resolves on PATH, and every additionalDirectories entry it requires is present in a tracked
// (not merely user-scope) settings file. All five findings land in one Report.
//
// Calibrate is the mandatory gate a caller MUST pass before running Phase 3's metered matrix: it
// blocks on any Report error, returning a named CalibrationBlockedError rather than letting an
// unconformant plugin reach a paid model run.
package plugin_conform
