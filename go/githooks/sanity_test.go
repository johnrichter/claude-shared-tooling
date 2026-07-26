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
var (
	fixtureAWSKey = "AKIA" + "ABCDEFGHIJKLMNOP" // AKIA + 16 chars -> matches the AWS key pattern
	fixturePEMKey = "-----BEGIN " + "RSA PRIVATE " + "KEY-----"
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

	got, err := ScanSecrets(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "src/leak.txt" || got[0].Rule != "aws_access_key_id" {
		t.Fatalf("got %+v, want one aws_access_key_id finding at src/leak.txt", got)
	}
}

// TestScanSecretsCleanFixturePasses confirms an all-clean tree yields no
// findings at all.
func TestScanSecretsCleanFixturePasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "README.md", "# Project\n\nNo secrets here.\n")
	writeFile(t, dir, "src/main.go", "package main\n\nfunc main() {}\n")

	got, err := ScanSecrets(dir, DefaultSkipRules)
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

	got, err := ScanSecrets(dir, DefaultSkipRules)
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

// TestScanPrivacyTierIsParameterized confirms the same file fails under the
// stricter public tier and passes under the looser datadog and personal
// tiers - the tier is a caller-supplied parameter, not a hardcoded value.
func TestScanPrivacyTierIsParameterized(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\n---\n\nbody\n")

	pubFail, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy(public): %v", err)
	}
	if len(pubFail) == 0 {
		t.Fatalf("want a public-tier failure for privacy:confidential")
	}

	ddFail, _, err := ScanPrivacy(dir, TierDatadog, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy(datadog): %v", err)
	}
	if len(ddFail) == 0 {
		t.Fatalf("want a datadog-tier failure for privacy:confidential")
	}

	personalFail, _, err := ScanPrivacy(dir, TierPersonal, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy(personal): %v", err)
	}
	if len(personalFail) != 0 {
		t.Fatalf("got %+v, want personal tier to allow any privacy value", personalFail)
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

// TestScanPrivacyCleanFixturePasses confirms a clean, public-tier-compliant
// file yields no failures and no warnings.
func TestScanPrivacyCleanFixturePasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: public\nowner: public\n---\n\nNothing sensitive.\n")

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
