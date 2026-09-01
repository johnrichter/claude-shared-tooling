package githooks

import "testing"

// fixtureFakeKeyBodyA and fixtureFakeKeyBodyB are two distinct, fabricated
// 40+ character base64-alphabet bodies - never a real vendor key shape -
// used to prove the caller-supplied ScanSecrets allowlist exempts only what
// it names.
const (
	fixtureFakeKeyBodyA = "MIIEpAIBAAKCAQEA1111111111abcdefghijklmnopqrstuvwxyzABCDEFGH"
	fixtureFakeKeyBodyB = "MIIEpAIBAAKCAQEA2222222222abcdefghijklmnopqrstuvwxyzABCDEFGH"
)

// TestScanSecretsExtraAllowlistNoEntryStillFlags confirms a fabricated,
// real-shaped fake key with no caller-supplied allowlist is flagged as
// before - the baseline this whole test file's other cases are relative to.
func TestScanSecretsExtraAllowlistNoEntryStillFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.pem", fixturePrivateKeyHeader+"\n"+fixtureFakeKeyBodyA+"\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "private_key_block" {
		t.Fatalf("got %+v, want one private_key_block finding with no allowlist", got)
	}
}

// TestScanSecretsExtraAllowlistExactValueExempts confirms naming the exact
// matched occurrence text in a caller-supplied Value entry exempts it.
func TestScanSecretsExtraAllowlistExactValueExempts(t *testing.T) {
	dir := t.TempDir()
	occurrence := fixturePrivateKeyHeader + "\n" + fixtureFakeKeyBodyA
	writeFile(t, dir, "leak.pem", occurrence+"\n")

	allowlist := []BetterleaksAllowlistEntry{{RuleID: "private_key_block", Value: occurrence}}
	got, err := ScanSecrets(dir, DefaultSkipRules, nil, allowlist)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings once the exact occurrence is allowlisted", got)
	}
}

// TestScanSecretsExtraAllowlistDoesNotOverMatch confirms a different,
// unallowlisted fake key body is still flagged in the same run: the
// allowlist exempts only the exact value it names, not every occurrence of
// the pattern.
func TestScanSecretsExtraAllowlistDoesNotOverMatch(t *testing.T) {
	dir := t.TempDir()
	allowedOccurrence := fixturePrivateKeyHeader + "\n" + fixtureFakeKeyBodyA
	otherOccurrence := fixturePrivateKeyHeader + "\n" + fixtureFakeKeyBodyB
	writeFile(t, dir, "allowed.pem", allowedOccurrence+"\n")
	writeFile(t, dir, "other.pem", otherOccurrence+"\n")

	allowlist := []BetterleaksAllowlistEntry{{RuleID: "private_key_block", Value: allowedOccurrence}}
	got, err := ScanSecrets(dir, DefaultSkipRules, nil, allowlist)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "other.pem" {
		t.Fatalf("got %+v, want only other.pem flagged, allowed.pem allowlisted", got)
	}
}

// TestScanSecretsExtraAllowlistRegexExempts confirms a Regex-based entry
// exempts every occurrence it matches, the same way a Value entry exempts
// one exact occurrence.
func TestScanSecretsExtraAllowlistRegexExempts(t *testing.T) {
	dir := t.TempDir()
	occurrenceA := fixturePrivateKeyHeader + "\n" + fixtureFakeKeyBodyA
	occurrenceB := fixturePrivateKeyHeader + "\n" + fixtureFakeKeyBodyB
	writeFile(t, dir, "a.pem", occurrenceA+"\n")
	writeFile(t, dir, "b.pem", occurrenceB+"\n")

	allowlist := []BetterleaksAllowlistEntry{{RuleID: "private_key_block", Regex: `BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA`}}
	got, err := ScanSecrets(dir, DefaultSkipRules, nil, allowlist)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings: the regex matches both fabricated bodies", got)
	}
}

// TestScanSecretsExtraAllowlistRuleIDScoping confirms an allowlist entry
// scoped to one rule id never exempts an occurrence of a different pattern,
// and that "*" (or an empty RuleID) applies across every pattern.
func TestScanSecretsExtraAllowlistRuleIDScoping(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "aws.txt", "aws_key = "+fixtureAWSKey+"\n")

	scoped := []BetterleaksAllowlistEntry{{RuleID: "private_key_block", Value: fixtureAWSKey}}
	got, err := ScanSecrets(dir, DefaultSkipRules, nil, scoped)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "aws_access_key_id" {
		t.Fatalf("got %+v, want the AWS finding still reported - the allowlist entry is scoped to a different rule", got)
	}

	wildcard := []BetterleaksAllowlistEntry{{RuleID: "*", Value: fixtureAWSKey}}
	got, err = ScanSecrets(dir, DefaultSkipRules, nil, wildcard)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings - a \"*\" RuleID entry applies to every pattern", got)
	}
}

// TestScanSecretsExtraAllowlistInvalidEntryErrors confirms an entry setting
// neither, or both, of Value/Regex is rejected before any file is scanned.
func TestScanSecretsExtraAllowlistInvalidEntryErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "aws.txt", "aws_key = "+fixtureAWSKey+"\n")

	for _, entry := range []BetterleaksAllowlistEntry{
		{RuleID: "aws_access_key_id"},
		{RuleID: "aws_access_key_id", Value: fixtureAWSKey, Regex: fixtureAWSKey},
	} {
		if _, err := ScanSecrets(dir, DefaultSkipRules, nil, []BetterleaksAllowlistEntry{entry}); err == nil {
			t.Fatalf("entry %+v: want an error, got nil", entry)
		}
	}
}

// TestScanSecretsExtraAllowlistInvalidRegexErrors confirms an entry whose
// Regex fails to compile is rejected before any file is scanned.
func TestScanSecretsExtraAllowlistInvalidRegexErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "aws.txt", "aws_key = "+fixtureAWSKey+"\n")

	allowlist := []BetterleaksAllowlistEntry{{RuleID: "aws_access_key_id", Regex: "[unterminated"}}
	if _, err := ScanSecrets(dir, DefaultSkipRules, nil, allowlist); err == nil {
		t.Fatal("want an error for an invalid Regex, got nil")
	}
}
