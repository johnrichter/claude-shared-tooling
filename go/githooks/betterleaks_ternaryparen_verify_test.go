package githooks

import (
	"strings"
	"testing"
)

// ─── Test-engineer verification: d79d587 ternary-filter-parenthesization fix ───
//
// The implementer's own TestScanCredentialsRuleScopedAllowlistSuppressesPIIFinancialFinding
// (betterleaks_test.go) only exercises pii-ssn. These tests independently
// prove the same fix generalizes to the other two affected rules
// (financial-credit-card-number, financial-iban) and that the claimed
// equivalence between targetRules-scoped and global ("*") allowlist
// exemptions actually holds post-fix. Every literal below is fictional and
// constructed by this test file (never a real card/account number, never a
// publicly documented vendor test value), computed to satisfy each rule's
// own checksum so a near-miss/checksum-invalid variant is never needed here
// (checksum-invalid near-miss coverage already exists in
// TestScanCredentialsBaseConfigPIIFinancialRulesFireAndCategorize).

var (
	// fixtureVerifyCard{A,B} are two distinct, fictional, Luhn(mod-10)-valid
	// 16-digit Visa-shaped (regex-matching) numbers, computed by this file's
	// author -- not a copied industry test number -- fragment-assembled per
	// this package's own convention.
	fixtureVerifyCardA = "412345678901234" + "9" // Luhn check digit computed for payload 412345678901234
	fixtureVerifyCardB = "432109876543210" + "7" // distinct payload, independently Luhn-valid

	// fixtureVerifyIBAN{A,B} are two distinct, fictional, mod-97-valid GB
	// IBANs using a fictional four-letter bank code ("ABCD"/"WXYZ", neither
	// a real bank identifier), check digits computed by this file's author.
	fixtureVerifyIBANA = "GB42ABCD1234561234" + "5678"
	fixtureVerifyIBANB = "GB14WXYZ9876541012" + "3456"
)

func TestScanCredentialsRuleScopedAllowlistSuppressesCreditCardFinding(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", strings.Join([]string{
		"card: " + fixtureVerifyCardA,
		"card: " + fixtureVerifyCardB,
	}, "\n")+"\n")

	baseline, err := ScanCredentials(dir, bin, BetterleaksOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanCredentials (baseline): %v", err)
	}
	if len(baseline) != 2 {
		t.Fatalf("baseline: got %+v, want both cards flagged with no allowlist entry", baseline)
	}

	opts := BetterleaksOptions{
		SkipRules:      DefaultSkipRules,
		ExtraAllowlist: []BetterleaksAllowlistEntry{{RuleID: "financial-credit-card-number", Value: fixtureVerifyCardA}},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "financial-credit-card-number" {
		t.Fatalf("got %+v, want exactly one financial-credit-card-number finding left (the un-exempted card)", got)
	}
}

func TestScanCredentialsRuleScopedAllowlistSuppressesIBANFinding(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", strings.Join([]string{
		"iban: " + fixtureVerifyIBANA,
		"iban: " + fixtureVerifyIBANB,
	}, "\n")+"\n")

	baseline, err := ScanCredentials(dir, bin, BetterleaksOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanCredentials (baseline): %v", err)
	}
	if len(baseline) != 2 {
		t.Fatalf("baseline: got %+v, want both IBANs flagged with no allowlist entry", baseline)
	}

	opts := BetterleaksOptions{
		SkipRules:      DefaultSkipRules,
		ExtraAllowlist: []BetterleaksAllowlistEntry{{RuleID: "financial-iban", Value: fixtureVerifyIBANA}},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "financial-iban" {
		t.Fatalf("got %+v, want exactly one financial-iban finding left (the un-exempted IBAN)", got)
	}
}

// TestScanCredentialsGlobalAllowlistSuppressesSSNFinding closes the loop on
// pii-ssn's global-scope leg of the same equivalence claim: the header
// comment's "confirmed live" statement for pii-ssn was a one-time manual
// check during development, not a permanent regression test, so this pins
// it the same way the other two rules are pinned below.
func TestScanCredentialsGlobalAllowlistSuppressesSSNFinding(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", strings.Join([]string{
		"ssn: " + fixtureBaseConfigSSN,
		"ssn: " + fixtureBaseConfigSSNOther,
	}, "\n")+"\n")

	opts := BetterleaksOptions{
		SkipRules:      DefaultSkipRules,
		ExtraAllowlist: []BetterleaksAllowlistEntry{{RuleID: "*", Value: fixtureBaseConfigSSNOther}},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "pii-ssn" {
		t.Fatalf("got %+v, want exactly one pii-ssn finding left (the un-exempted SSN, global \"*\" scope)", got)
	}
}

// TestScanCredentialsGlobalAllowlistSuppressesCreditCardFinding proves the
// header comment's claim that a global rule_id "*" exemption is equivalent
// to a targetRules-scoped one post-fix, for financial-credit-card-number
// (the implementer's own live-verification claim covered pii-ssn; this
// checks a second, independent rule rather than trusting the claimed
// equivalence).
func TestScanCredentialsGlobalAllowlistSuppressesCreditCardFinding(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", strings.Join([]string{
		"card: " + fixtureVerifyCardA,
		"card: " + fixtureVerifyCardB,
	}, "\n")+"\n")

	opts := BetterleaksOptions{
		SkipRules:      DefaultSkipRules,
		ExtraAllowlist: []BetterleaksAllowlistEntry{{RuleID: "*", Value: fixtureVerifyCardB}},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "financial-credit-card-number" {
		t.Fatalf("got %+v, want exactly one financial-credit-card-number finding left (the un-exempted card, global \"*\" scope)", got)
	}
}

// TestScanCredentialsGlobalAllowlistSuppressesIBANFinding is the third and
// last leg of that same equivalence check, for financial-iban -- the header
// comment's "confirmed equivalent for these three rules" claim is only as
// good as an independent check of every rule it names, not just one.
func TestScanCredentialsGlobalAllowlistSuppressesIBANFinding(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", strings.Join([]string{
		"iban: " + fixtureVerifyIBANA,
		"iban: " + fixtureVerifyIBANB,
	}, "\n")+"\n")

	opts := BetterleaksOptions{
		SkipRules:      DefaultSkipRules,
		ExtraAllowlist: []BetterleaksAllowlistEntry{{RuleID: "*", Value: fixtureVerifyIBANB}},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "financial-iban" {
		t.Fatalf("got %+v, want exactly one financial-iban finding left (the un-exempted IBAN, global \"*\" scope)", got)
	}
}
