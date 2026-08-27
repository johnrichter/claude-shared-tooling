// Package githooks is the canonical set of pre-commit content guardrails:
// ScanSecrets (plaintext secrets), ScanRawBinary (should-be-LFS raw binaries
// and committed executables) and ScanPrivacy (tier-parameterized
// frontmatter/PII markers). One scanner per guardrail, shared by every repo's
// hook and CI wiring instead of a per-repo copy. Scan results compose into a
// clikit result record via BuildHookResult, so a caller's hook binary or CI
// job has one line to call to get a well-formed JSON envelope on stdout.
// File discovery is built on fsx's injected-ruleset path classification:
// which directories/paths a scan skips is a caller-supplied parameter, not a
// hardcoded list. Path exemption is per-check, never global: beyond skipRules
// (a path no scan reads at all), ScanPrivacy takes MarkerExemptRules and
// SecretExemptRules, each suppressing only its own check, and ScanSecrets
// takes that same secret-exempt ruleset - so exempting a path from one check
// never exempts it from another. An exempt ruleset carrying a pattern that is
// not a valid glob is a hard error, returned before any file is read, never a
// silent tree-wide exemption.
//
// ScanRawBinary carries two independent rules over the same candidate list,
// both gated by skipRules and isLFSRouted:
//
//   - "raw_binary" fires on a candidate over the caller's maxBytes whose
//     leading sniffBytes (8000) bytes are binary (a NUL byte, or content that
//     fails to decode as UTF-8).
//   - "raw_binary_executable" fires on a candidate checked out at an
//     executable file mode (git's tracked 100755) whose leading sniffBytes
//     bytes are binary, at any size - a committed, ready-to-exec build
//     output is a violation regardless of how small it is, so this rule is
//     not gated by maxBytes. A candidate that trips both rules yields two
//     findings.
//
// Both rules skip a candidate that no longer exists on disk (e.g. a staged
// deletion) rather than reporting or erroring, and both return an error
// (never panic) if a qualifying candidate's content cannot be read.
package githooks
