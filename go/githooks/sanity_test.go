package githooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// Trigger literals are assembled from fragments so this file's own source
// does not trip the repo's secret guardrail when it scans this tree.
// fixtureAWSDocKey is fragment-assembled too, even though this package and
// the repo's scripts/check_secrets.py guardrail both allowlist it by exact
// match: a pre-fix git-tools binary landing this very commit has no such
// allowlist yet, and its own whole-file scan of this source line would
// otherwise refuse the merge that ships the allowlist. The concatenation
// changes nothing about the value under test.
var (
	fixtureAWSKey          = "AKIA" + "ABCDEFGHIJKLMNOP"    // AKIA + 16 chars -> matches the AWS key pattern
	fixtureAWSDocKey       = "AKIAIOSFODNN7" + "EXAMPLE"    // AWS's reserved doc placeholder -> allowlisted
	fixtureAWSNearMiss     = "AKIAIOSFODNN7EXAMPL" + "F"    // one char off the placeholder -> not allowlisted
	fixtureSlackDocToken   = "xoxb-ab59" + "EXAMPLETOKEN"   // a scanner tool's own documented Slack-token example -> allowlisted
	fixtureSlackRealShaped = "xoxb-ab59" + "REALLOOKINGABC" // same prefix and length class, not the placeholder -> not allowlisted
	fixturePEMKey          = "-----BEGIN " + "RSA PRIVATE " + "KEY-----"
)

func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestScanSecretsDetectsPlantedSecret confirms a planted AWS access-key id is
// found by rule and path, and an unrelated clean file yields no findings.
func TestScanSecretsDetectsPlantedSecret(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/leak.txt", "aws_key = "+fixtureAWSKey+"\n")
	writeFile(t, dir, "src/clean.txt", "nothing to see here\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/leak.txt" || got[0].Rule != "aws_access_key_id" {
		t.Fatalf("got %+v, want one aws_access_key_id finding at src/leak.txt", got)
	}
}

// TestScanSecretsExemptsAWSDocPlaceholder confirms AWS's own canonical,
// permanently-reserved documentation placeholder access-key id never
// triggers a finding, so it stops false-positiving in a corpus that quotes
// AWS's official docs/examples.
func TestScanSecretsExemptsAWSDocPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/example.md", "aws_access_key_id = "+fixtureAWSDocKey+"\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings for the AWS doc placeholder key", got)
	}
}

// TestScanSecretsStillFlagsRealShapedKey confirms the placeholder exemption
// is exact, not a weakening of the general AWS-key-shape detection: a
// different, plausible-looking key still triggers.
func TestScanSecretsStillFlagsRealShapedKey(t *testing.T) {
	dir := t.TempDir()
	fixtureOtherKey := "AKIA" + "JXNH2K3LQZABCDEF" // AKIA + 16 chars, not the placeholder
	writeFile(t, dir, "src/leak.txt", "aws_key = "+fixtureOtherKey+"\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/leak.txt" || got[0].Rule != "aws_access_key_id" {
		t.Fatalf("got %+v, want one aws_access_key_id finding at src/leak.txt", got)
	}
}

// TestScanSecretsExemptsSlackDocPlaceholder confirms a third-party scanner
// tool's own documented Slack-token example format never triggers a finding,
// so it stops false-positiving in a corpus that ingests that tool's own
// rule-definition file.
func TestScanSecretsExemptsSlackDocPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/example.md", "Example of matching format: `"+fixtureSlackDocToken+"`\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings for the Slack doc placeholder token", got)
	}
}

// TestScanSecretsStillFlagsRealShapedSlackToken confirms the placeholder
// exemption is exact, not a weakening of the general Slack-token-shape
// detection: a different token of the same prefix and length class still
// triggers.
func TestScanSecretsStillFlagsRealShapedSlackToken(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/leak.txt", "slack_token = "+fixtureSlackRealShaped+"\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/leak.txt" || got[0].Rule != "slack_token" {
		t.Fatalf("got %+v, want one slack_token finding at src/leak.txt", got)
	}
}

// TestScanSecretsStillFlagsSlackPlaceholderWithAppendedChars pins a boundary
// case specific to the Slack pattern, which the AWS tests cannot cover: the
// Slack regex has no trailing \b, so a longer token that merely starts with
// the placeholder must still be flagged. Greedy matching consumes the whole
// token-character run, so the compared match is not the exempt string.
func TestScanSecretsStillFlagsSlackPlaceholderWithAppendedChars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/leak.txt", "slack_token = "+fixtureSlackDocToken+"DEADBEEF\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/leak.txt" || got[0].Rule != "slack_token" {
		t.Fatalf("got %+v, want one slack_token finding at src/leak.txt", got)
	}
}

// TestScanSecretsStillFlagsNearMissOfPlaceholder confirms the exemption is a
// strict exact match, not fuzzy: a single-character near-miss of the
// placeholder still triggers.
func TestScanSecretsStillFlagsNearMissOfPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src/leak.txt", "aws_key = "+fixtureAWSNearMiss+"\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/leak.txt" || got[0].Rule != "aws_access_key_id" {
		t.Fatalf("got %+v, want one aws_access_key_id finding at src/leak.txt", got)
	}
}

// TestScanSecretsRealKeyAlongsidePlaceholderStillFlagged pins the exemption's
// per-occurrence semantics: matchesSecretPattern flags a file if ANY occurrence
// is non-exempt, so a file that legitimately quotes the placeholder and also
// leaks a real-shaped key is still reported. Without this case, inverting the
// helper to "exempt the file if any occurrence is exempt" would leave every
// other test green.
func TestScanSecretsRealKeyAlongsidePlaceholderStillFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/mixed.md", "example: "+fixtureAWSDocKey+"\nreal: "+fixtureAWSKey+"\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "docs/mixed.md" || got[0].Rule != "aws_access_key_id" {
		t.Fatalf("got %+v, want one aws_access_key_id finding at docs/mixed.md", got)
	}
}

// TestScanSecretsPlaceholderWithoutWordBoundaryIsNotExempted confirms the
// allowlist is applied only to what the AWS-key regex actually extracts
// (a \b...\b-bounded match), never by a raw substring search: the
// placeholder glued onto other word characters, so that \b never matches at
// either end, is not a regex hit at all - and is therefore not reported,
// exactly as an equally-embedded real key would not be either. This proves
// the exemption cannot accidentally suppress a genuine leaked key merely
// because it happens to contain the placeholder text as a substring.
func TestScanSecretsPlaceholderWithoutWordBoundaryIsNotExempted(t *testing.T) {
	dir := t.TempDir()
	// Neither embedding has a \b boundary immediately before or after the
	// placeholder run (word chars on both sides), so the AWS pattern itself
	// never matches these strings - independent of the allowlist.
	writeFile(t, dir, "src/a.txt", "x"+fixtureAWSDocKey+"x\n")
	writeFile(t, dir, "src/b.txt", fixtureAWSDocKey+"123\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings: the AWS key regex has no word-boundary match in either fixture, so nothing is extracted for the allowlist to even consider", got)
	}
}

// TestScanSecretsCleanFixturePasses confirms an all-clean tree yields no
// findings at all.
func TestScanSecretsCleanFixturePasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Project\n\nNo secrets here.\n")
	writeFile(t, dir, "src/main.go", "package main\n\nfunc main() {}\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings on a clean fixture", got)
	}
}

// TestScanSecretsSkipsExcludedDirs confirms a secret planted under a
// skip-classified directory is never reported.
func TestScanSecretsSkipsExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "node_modules/pkg/secret.txt", fixturePEMKey+"\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want skip-ruled directory excluded", got)
	}
}

// TestScanRawBinaryDetectsOversizeNonLFSBinary confirms an oversize binary
// candidate not routed through LFS is reported, while an LFS-routed one and
// an undersized one are not.
func TestScanRawBinaryDetectsOversizeNonLFSBinary(t *testing.T) {
	dir := t.TempDir()
	binary := append([]byte{0x00, 0x01, 0x02}, bytes.Repeat([]byte{0xff}, 200)...)
	writeFile(t, dir, "assets/blob.bin", string(binary))
	writeFile(t, dir, "assets/small.bin", string(binary[:10]))
	writeFile(t, dir, "assets/routed.bin", string(binary))

	lfsRouted := func(rel string) (bool, error) { return rel == "assets/routed.bin", nil }
	got, err := ScanRawBinary(dir, []string{"assets/blob.bin", "assets/small.bin", "assets/routed.bin"}, DefaultSkipRules, 100, lfsRouted)
	if err != nil {
		t.Fatalf("ScanRawBinary: %v", err)
	}
	if len(got) != 1 || got[0].Path != "assets/blob.bin" || got[0].Rule != "raw_binary" {
		t.Fatalf("got %+v, want one raw_binary finding at assets/blob.bin", got)
	}
}

// TestScanRawBinaryCleanFixturePasses confirms a text-only candidate set
// yields no findings regardless of size.
func TestScanRawBinaryCleanFixturePasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/big.txt", string(bytes.Repeat([]byte("a"), 200)))

	got, err := ScanRawBinary(dir, []string{"docs/big.txt"}, DefaultSkipRules, 100, nil)
	if err != nil {
		t.Fatalf("ScanRawBinary: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings for a text candidate", got)
	}
}

// TestScanPrivacyTierIsParameterized confirms the same privacy:private file
// fails under both the public and confidential tiers - private is more
// sensitive than either - and passes only under the private tier itself: the
// tier is a caller-supplied parameter, not a hardcoded value.
func TestScanPrivacyTierIsParameterized(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: private\n---\n\nbody\n")

	pubFail, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy(public): %v", err)
	}
	if len(pubFail) == 0 {
		t.Fatalf("want a public-tier failure for privacy:private")
	}

	confFail, _, err := ScanPrivacy(dir, TierConfidential, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy(confidential): %v", err)
	}
	if len(confFail) == 0 {
		t.Fatalf("want a confidential-tier failure for privacy:private")
	}

	privateFail, _, err := ScanPrivacy(dir, TierPrivate, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy(private): %v", err)
	}
	if len(privateFail) != 0 {
		t.Fatalf("got %+v, want private tier to allow any privacy value", privateFail)
	}
}

// TestScanPrivacyConfidentialTierAllowsOwnConfidentialMarker confirms a
// confidential-tier repo's own privacy:confidential frontmatter tag is not a
// violation - it matches the repo's own declared posture, not a more
// sensitive one.
func TestScanPrivacyConfidentialTierAllowsOwnConfidentialMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\n---\n\nbody\n")

	failures, _, err := ScanPrivacy(dir, TierConfidential, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("got %+v, want no failure for privacy:confidential at the confidential tier", failures)
	}
}

// TestScanPrivacyPublicTierForbidsPrivateMarker confirms the public tier
// catches the most sensitive value even when it appears alone, without also
// needing an intervening confidential tag - the gap this fix closes.
func TestScanPrivacyPublicTierForbidsPrivateMarker(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: private\n---\n\nbody\n")

	failures, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	var sawMarker bool
	for _, f := range failures {
		if f.Rule == "forbidden_marker" {
			sawMarker = true
		}
	}
	if !sawMarker {
		t.Fatalf("got %+v, want a forbidden_marker failure for privacy:private at the public tier", failures)
	}
}

// TestScanPrivacyPublicTierRequiresPublicPair confirms the public tier fails
// a file that declares privacy: but not privacy:public - not just the
// explicitly forbidden values.
func TestScanPrivacyPublicTierRequiresPublicPair(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: restricted\n---\n\nbody\n")

	got, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("want a failure for an unenumerated non-public privacy value")
	}
}

// TestScanPrivacyMarkerExemptDirSkipsMarkerCheckOnly confirms a marker inside
// a marker-exempt directory's frontmatter is not flagged, while a secret in
// the same file still is.
func TestScanPrivacyMarkerExemptDirSkipsMarkerCheckOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "fixtures/case.md", "---\nprivacy: confidential\n---\n\n"+fixtureAWSKey+"\n")
	exempt := []fsx.Rule{{Pattern: "fixtures/**", Class: SkipClass}}

	failures, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules, MarkerExemptRules: exempt})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	sawMarker, sawSecret := false, false
	for _, f := range failures {
		if f.Rule == "forbidden_marker" {
			sawMarker = true
		}
		if f.Rule == "aws_access_key_id" {
			sawSecret = true
		}
	}
	if sawMarker {
		t.Fatalf("got %+v, want marker check skipped under a marker-exempt dir", failures)
	}
	if !sawSecret {
		t.Fatalf("got %+v, want the secret still caught (whole-file, no exemption)", failures)
	}
}

// TestScanPrivacySecretExemptDirSkipsSecretCheckOnly confirms a secret inside
// a secret-exempt directory is not flagged, while a marker violation and an
// internal-identifier hit in that same file still are - the narrow proof that
// SecretExemptRules never leaks into the other two checks.
func TestScanPrivacySecretExemptDirSkipsSecretCheckOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "corpus/case.md", "---\nprivacy: confidential\n---\n\n"+
		fixtureAWSKey+"\nsee host.corp for details\n")
	exempt := []fsx.Rule{{Pattern: "corpus/**", Class: SkipClass}}

	failures, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules, SecretExemptRules: exempt})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	sawMarker, sawSecret := false, false
	for _, f := range failures {
		if f.Rule == "forbidden_marker" {
			sawMarker = true
		}
		if f.Rule == "aws_access_key_id" {
			sawSecret = true
		}
	}
	if sawSecret {
		t.Fatalf("got %+v, want secret check skipped under a secret-exempt dir", failures)
	}
	if !sawMarker {
		t.Fatalf("got %+v, want the marker violation still caught", failures)
	}
	if len(warnings) == 0 {
		t.Fatalf("want the internal-identifier email still caught as a warning")
	}
}

// TestScanPrivacySecretExemptDirIsPathScoped confirms an identical secret
// planted outside the secret-exempt directory is still reported in the same
// scan - the exemption is path-scoped, not a global weakening of the check.
func TestScanPrivacySecretExemptDirIsPathScoped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "corpus/case.md", fixtureAWSKey+"\n")
	writeFile(t, dir, "src/leak.md", fixtureAWSKey+"\n")
	exempt := []fsx.Rule{{Pattern: "corpus/**", Class: SkipClass}}

	failures, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules, SecretExemptRules: exempt})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	var flaggedPaths []string
	for _, f := range failures {
		if f.Rule == "aws_access_key_id" {
			flaggedPaths = append(flaggedPaths, f.Path)
		}
	}
	if len(flaggedPaths) != 1 || flaggedPaths[0] != "src/leak.md" {
		t.Fatalf("got %+v, want only src/leak.md flagged, not corpus/case.md", flaggedPaths)
	}
}

// TestScanPrivacyHonorsAWSDocPlaceholderAllowlist pins the placeholder
// allowlist at ScanPrivacy's own inline secret loop, which is a second call
// site into the pattern set that ScanSecrets' tests do not cover: without this
// case, reverting that loop to a bare regex match - reintroducing the exact
// false positive this branch fixes, for every ScanPrivacy consumer - leaves
// the whole suite green. The real-shaped key in the same scan keeps the case
// from passing merely because the secret check stopped running.
func TestScanPrivacyHonorsAWSDocPlaceholderAllowlist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "docs/example.md", "aws_access_key_id = "+fixtureAWSDocKey+"\n")
	writeFile(t, dir, "src/leak.md", fixtureAWSKey+"\n")

	failures, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	var flaggedPaths []string
	for _, f := range failures {
		if f.Rule == "aws_access_key_id" {
			flaggedPaths = append(flaggedPaths, f.Path)
		}
	}
	if len(flaggedPaths) != 1 || flaggedPaths[0] != "src/leak.md" {
		t.Fatalf("got %+v, want only src/leak.md flagged, never the AWS doc placeholder at docs/example.md", flaggedPaths)
	}
}

// TestScanSecretsHonorsSecretExemptRules confirms ScanSecrets itself - not
// just ScanPrivacy's inline secret check - skips a secret-exempt path, and
// still reports the identical pattern at a non-exempt path in the same run.
func TestScanSecretsHonorsSecretExemptRules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "corpus/case.md", fixtureAWSKey+"\n")
	writeFile(t, dir, "src/leak.md", fixtureAWSKey+"\n")
	exempt := []fsx.Rule{{Pattern: "corpus/**", Class: SkipClass}}

	got, err := ScanSecrets(dir, DefaultSkipRules, exempt)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/leak.md" || got[0].Rule != "aws_access_key_id" {
		t.Fatalf("got %+v, want one aws_access_key_id finding at src/leak.md only", got)
	}
}

// TestScanPrivacyCleanFixturePasses confirms a clean, public-tier-compliant
// file yields no failures and no warnings.
func TestScanPrivacyCleanFixturePasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: public\n---\n\nNothing sensitive.\n")

	failures, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(failures) != 0 || len(warnings) != 0 {
		t.Fatalf("got failures=%+v warnings=%+v, want a clean fixture to pass", failures, warnings)
	}
}

// TestBuildHookResultSuccessIsWellFormed confirms a clean scan builds a
// success record that canonicalizes cleanly.
func TestBuildHookResultSuccessIsWellFormed(t *testing.T) {
	result, err := BuildHookResult([]string{"githooks", "scan"}, ScanOutcome{})
	if err != nil {
		t.Fatalf("BuildHookResult: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	assertCanonicalJSON(t, result)
}

// TestBuildHookResultFailureIsWellFormed confirms a scan with findings across
// all three guardrails builds a precondition_unmet record, one governing
// error per finding, that canonicalizes and round-trips as valid JSON.
func TestBuildHookResultFailureIsWellFormed(t *testing.T) {
	outcome := ScanOutcome{
		Secrets:   []Finding{{Path: "a.txt", Rule: "aws_access_key_id", Detail: "possible aws access key id"}},
		RawBinary: []Finding{{Path: "b.bin", Rule: "raw_binary", Detail: "raw binary (200 bytes, over 100-byte threshold, not LFS-routed)"}},
		PrivacyFailures: []Finding{
			{Path: "c.md", Rule: "forbidden_marker", Detail: `forbidden frontmatter marker "privacy: confidential"`},
		},
	}
	result, err := BuildHookResult([]string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("BuildHookResult: %v", err)
	}
	if result.ExitCode != 30 {
		t.Fatalf("ExitCode = %d, want 30 (precondition_unmet)", result.ExitCode)
	}
	if len(result.Errors) != 3 {
		t.Fatalf("got %d errors, want one per finding (3)", len(result.Errors))
	}
	assertCanonicalJSON(t, result)
}

// TestBuildHookResultWarningsOnlyIsCaveats confirms privacy warnings alone
// (no failures, non-strict) build a caveats record, not success or failure.
func TestBuildHookResultWarningsOnlyIsCaveats(t *testing.T) {
	outcome := ScanOutcome{
		PrivacyWarnings: []Finding{{Path: "a.md", Rule: "internal_identifier", Detail: "internal identifier — internal hostname"}},
	}
	result, err := BuildHookResult([]string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("BuildHookResult: %v", err)
	}
	if result.ExitCode != 10 {
		t.Fatalf("ExitCode = %d, want 10 (caveats)", result.ExitCode)
	}
	assertCanonicalJSON(t, result)
}

// TestBuildHookResultStrictEscalatesWarnings confirms Strict promotes a
// privacy warning into a governing failure.
func TestBuildHookResultStrictEscalatesWarnings(t *testing.T) {
	outcome := ScanOutcome{
		PrivacyWarnings: []Finding{{Path: "a.md", Rule: "internal_identifier", Detail: "internal identifier — internal hostname"}},
		Strict:          true,
	}
	result, err := BuildHookResult([]string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("BuildHookResult: %v", err)
	}
	if result.ExitCode != 30 {
		t.Fatalf("ExitCode = %d, want 30 (precondition_unmet) once Strict escalates the warning", result.ExitCode)
	}
}

// TestEmitHookResultWritesCanonicalJSONLine confirms EmitHookResult writes
// exactly one LF-terminated canonical JSON line and returns its exit code.
func TestEmitHookResultWritesCanonicalJSONLine(t *testing.T) {
	var buf bytes.Buffer
	code, err := EmitHookResult(&buf, []string{"githooks", "scan"}, ScanOutcome{})
	if err != nil {
		t.Fatalf("EmitHookResult: %v", err)
	}
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	out := buf.Bytes()
	if out[len(out)-1] != '\n' || bytes.Count(out, []byte("\n")) != 1 {
		t.Fatalf("output not exactly one LF-terminated line: %q", out)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimRight(out, "\n"), &rec); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if rec["status"] != "success" {
		t.Fatalf("status = %v, want success", rec["status"])
	}
}

func assertCanonicalJSON(t *testing.T, result interface{ MarshalCanonical() ([]byte, error) }) {
	t.Helper()
	canon, err := result.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(canon, &rec); err != nil {
		t.Fatalf("MarshalCanonical output not valid JSON: %v", err)
	}
}
