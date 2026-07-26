package githooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// findRepoRoot returns the git worktree root containing this test file, so
// TestSecretsFileNotGitIgnored can check the real on-disk gitignore state
// rather than a synthetic fixture.
func findRepoRoot(t *testing.T) (string, error) {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// isPathGitIgnored reports whether git considers relPath ignored under root.
func isPathGitIgnored(t *testing.T, root, relPath string) bool {
	t.Helper()
	cmd := exec.Command("git", "check-ignore", "-q", relPath)
	cmd.Dir = root
	err := cmd.Run()
	return err == nil // exit 0 => ignored; exit 1 => not ignored; other => treated as not-ignored here
}

// Trigger literals assembled from fragments so this file's own source does
// not trip the repo's secret guardrail when it scans this tree.
var (
	fixtureSlackToken  = "xoxb-" + "1234567890-abcdefghij"
	fixtureGitHubToken = "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789AB" // 36+ chars after ghp_
)

// TestScanSecretsDetectsAllSignatureShapes confirms every closed signature —
// not just AWS keys — fires, and that a near-miss below the length/shape
// threshold does not.
func TestScanSecretsDetectsAllSignatureShapes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "slack.txt", "token: "+fixtureSlackToken+"\n")
	writeFile(t, dir, "gh.txt", "token: "+fixtureGitHubToken+"\n")
	writeFile(t, dir, "pem.txt", fixturePEMKey+"\n-----END RSA PRIVATE KEY-----\n")
	writeFile(t, dir, "near-miss.txt", "ghp_tooshort\nAKIA123\n") // too short for either signature

	got, err := ScanSecrets(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	rules := map[string]bool{}
	for _, f := range got {
		rules[f.Path+"|"+f.Rule] = true
	}
	for _, want := range []string{"slack.txt|slack_token", "gh.txt|github_token", "pem.txt|private_key_block"} {
		if !rules[want] {
			t.Errorf("missing expected finding %s in %+v", want, got)
		}
	}
	for _, f := range got {
		if f.Path == "near-miss.txt" {
			t.Errorf("near-miss.txt should not match any signature, got %+v", f)
		}
	}
}

// TestScanSecretsSkipsNonUTF8Binary confirms a file that fails to decode as
// UTF-8 is never scanned, even though a secret-shaped byte run happens to sit
// alongside the invalid bytes.
func TestScanSecretsSkipsNonUTF8Binary(t *testing.T) {
	dir := t.TempDir()
	invalid := append([]byte(fixtureAWSKey), 0xff, 0xfe, 0x00)
	writeFile(t, dir, "blob.dat", string(invalid))

	got, err := ScanSecrets(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want non-UTF8 file never scanned as text", got)
	}
}

// TestScanRawBinaryLFSRoutedFileNotReported confirms an over-threshold binary
// candidate that IS LFS-routed is excluded, and a caller-injected
// LFSRouteChecker error propagates rather than being silently swallowed.
func TestScanRawBinaryLFSRoutedFileNotReported(t *testing.T) {
	dir := t.TempDir()
	binary := append([]byte{0x00}, bytes.Repeat([]byte{0xff}, 200)...)
	writeFile(t, dir, "routed.bin", string(binary))

	always := func(rel string) (bool, error) { return true, nil }
	got, err := ScanRawBinary(dir, []string{"routed.bin"}, DefaultSkipRules, 100, always)
	if err != nil {
		t.Fatalf("ScanRawBinary: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want LFS-routed candidate excluded", got)
	}

	boom := errors.New("git check-attr failed")
	_, err = ScanRawBinary(dir, []string{"routed.bin"}, DefaultSkipRules, 100, func(rel string) (bool, error) { return false, boom })
	if err == nil {
		t.Fatal("want LFSRouteChecker error to propagate, got nil")
	}
}

// TestScanRawBinaryMissingCandidateSkipped confirms a candidate path that no
// longer exists on disk (e.g. a staged deletion) is skipped, not an error.
func TestScanRawBinaryMissingCandidateSkipped(t *testing.T) {
	dir := t.TempDir()
	got, err := ScanRawBinary(dir, []string{"gone.bin"}, DefaultSkipRules, 0, nil)
	if err != nil {
		t.Fatalf("ScanRawBinary: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want a missing candidate silently skipped", got)
	}
}

// TestScanRawBinarySkipRuleExcludesCandidate confirms a candidate whose path
// resolves to skipRules' SkipClass is never flagged even if it is a
// qualifying raw binary.
func TestScanRawBinarySkipRuleExcludesCandidate(t *testing.T) {
	dir := t.TempDir()
	binary := append([]byte{0x00}, bytes.Repeat([]byte{0xff}, 200)...)
	writeFile(t, dir, "vendor/blob.bin", string(binary))

	got, err := ScanRawBinary(dir, []string{"vendor/blob.bin"}, []fsx.Rule{{Pattern: "vendor/**", Class: SkipClass}}, 100, nil)
	if err != nil {
		t.Fatalf("ScanRawBinary: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want skip-ruled candidate excluded", got)
	}
}

// TestScanPrivacyUnknownTierErrors confirms an unrecognized tier is a hard
// error, not a silent no-op scan.
func TestScanPrivacyUnknownTierErrors(t *testing.T) {
	dir := t.TempDir()
	_, _, err := ScanPrivacy(dir, PrivacyTier("bogus"), PrivacyOptions{})
	if err == nil {
		t.Fatal("want an error for an unknown privacy tier, got nil")
	}
}

// TestScanPrivacyPersonalTierAllowsInternalMarkers confirms the loosest tier
// raises neither a forbidden-marker failure nor an internal-identifier
// warning for content the public/datadog tiers would flag.
func TestScanPrivacyPersonalTierAllowsInternalMarkers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\nowner: personal\n---\n\nsee host.corp and jane@datadoghq.com\n")

	failures, warnings, err := ScanPrivacy(dir, TierPersonal, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(failures) != 0 || len(warnings) != 0 {
		t.Fatalf("got failures=%+v warnings=%+v, want personal tier fully permissive", failures, warnings)
	}
}

// TestScanPrivacyDatadogTierAllowsInternalHostnameButFlagsPrivateNetwork
// confirms the relaxed datadog-tier internal-id posture: an internal
// hostname/email is expected in an org-shared repo (no warning), but a
// private-network URL is still flagged.
func TestScanPrivacyDatadogTierAllowsInternalHostnameButFlagsPrivateNetwork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "see host.corp and jane@datadoghq.com and http://10.0.0.5/admin\n")

	_, warnings, err := ScanPrivacy(dir, TierDatadog, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 || warnings[0].Rule != "internal_identifier" {
		t.Fatalf("got %+v, want exactly one private-network warning under datadog tier", warnings)
	}
}

// TestScanPrivacyPublicEmailAllowlistExemptByExactAddressOnly confirms an
// enumerated public role address at the org domain is never flagged, while a
// different address at the same domain still is.
func TestScanPrivacyPublicEmailAllowlistExemptByExactAddressOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact support@datadoghq.com or jane@datadoghq.com\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %+v, want exactly one warning (jane@), support@ allowlisted", warnings)
	}
}

// TestScanPrivacyReservedSentinelHostNotFlaggedAsPrivateNetwork confirms an
// RFC 6761 reserved sentinel host (example.com) is not mistaken for a
// private-network address, while a genuine private IP still is.
func TestScanPrivacyReservedSentinelHostNotFlaggedAsPrivateNetwork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "see http://192.168.1.1.example.com/x and http://192.168.1.1/x\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings %+v, want exactly one (the genuine private IP, not the .example.com host)", len(warnings), warnings)
	}
}

// TestBuildHookResultCapsAtFiftyDiagnosticsWithOverflowCaveat confirms a run
// with more failing findings than clikit's 50-entry diagnostic cap truncates
// errors to 50 and adds exactly one overflow-summarizing caveat, rather than
// failing to build a record or silently dropping the overflow count.
func TestBuildHookResultCapsAtFiftyDiagnosticsWithOverflowCaveat(t *testing.T) {
	var secrets []Finding
	for i := 0; i < 60; i++ {
		secrets = append(secrets, Finding{Path: fmt.Sprintf("f%d.txt", i), Rule: "aws_access_key_id", Detail: "possible aws access key id"})
	}
	result, err := BuildHookResult([]string{"githooks", "scan"}, ScanOutcome{Secrets: secrets})
	if err != nil {
		t.Fatalf("BuildHookResult: %v", err)
	}
	if len(result.Errors) != 50 {
		t.Fatalf("got %d errors, want capped at 50", len(result.Errors))
	}
	if len(result.Caveats) != 1 {
		t.Fatalf("got %d caveats, want exactly one overflow caveat", len(result.Caveats))
	}
	assertCanonicalJSON(t, result)
}

// TestEmitHookResultFailureEnvelopeRoundTripsAndValidates confirms a
// precondition_unmet envelope (the failure path, not just success) is valid
// JSON, exposes the finding under errors, and round-trips through the
// clikit-canonical form used for schema conformance.
func TestEmitHookResultFailureEnvelopeRoundTripsAndValidates(t *testing.T) {
	var buf bytes.Buffer
	outcome := ScanOutcome{Secrets: []Finding{{Path: "a.txt", Rule: "aws_access_key_id", Detail: "possible aws access key id"}}}
	code, err := EmitHookResult(&buf, []string{"githooks", "scan"}, outcome)
	if err != nil {
		t.Fatalf("EmitHookResult: %v", err)
	}
	if code != 30 {
		t.Fatalf("code = %d, want 30 (precondition_unmet)", code)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &rec); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if rec["status"] != "precondition_unmet" {
		t.Fatalf("status = %v, want precondition_unmet", rec["status"])
	}
	errs, ok := rec["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one diagnostic", rec["errors"])
	}
}

// TestSecretsFileNotGitIgnored is a packaging guard: the scanner source file
// implementing ScanSecrets must actually be trackable by git, or it silently
// never ships in a commit despite existing on disk and compiling locally.
func TestSecretsFileNotGitIgnored(t *testing.T) {
	root, err := findRepoRoot(t)
	if err != nil {
		t.Skipf("not inside a git worktree, skipping: %v", err)
	}
	target := filepath.Join(root, "go", "githooks", "secrets.go")
	if _, err := os.Stat(target); err != nil {
		t.Skipf("secrets.go not found at expected path, skipping: %v", err)
	}
	if isPathGitIgnored(t, root, "go/githooks/secrets.go") {
		t.Errorf("go/githooks/secrets.go matches a .gitignore rule and will never be committed " +
			"(repo's generic 'secrets.*' credential-file pattern shadows this source file name)")
	}
}
