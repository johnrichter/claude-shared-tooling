// Package githooks is the canonical set of pre-commit content guardrails:
// ScanSecrets (plaintext secrets), ScanRawBinary (should-be-LFS raw binaries)
// and ScanPrivacy (tier-parameterized frontmatter/PII markers). One scanner
// per guardrail, shared by every repo's hook and CI wiring instead of a
// per-repo copy. Scan results compose into a clikit result record via
// BuildHookResult, so a caller's hook binary or CI job has one line to call
// to get a well-formed JSON envelope on stdout. File discovery is built on
// fsx's injected-ruleset path classification: which directories/paths a scan
// skips is a caller-supplied parameter, not a hardcoded list.
package githooks
