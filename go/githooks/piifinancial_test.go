package githooks

import "testing"

// Trigger literals are assembled from fragments so this file's own source
// does not trip the repo's own secret guardrail(s), matching this package's
// existing convention (see sanity_test.go).
var (
	fixtureValidSSN       = "123-45-" + "6789"            // area/group/serial all in-range
	fixtureInvalidAreaSSN = "000-45-" + "6789"            // area 000 is never issued
	fixtureVisaTestCard   = "41111111111111" + "11"       // published Visa test number (Luhn-valid)
	fixtureMCTestCard     = "55555555555544" + "44"       // published Mastercard test number (Luhn-valid)
	fixtureRealVisaShape  = "412345678901234" + "9"       // Visa-shaped, distinct from the test number, Luhn-valid
	fixtureLuhnInvalid    = "4111111111111" + "12"        // Visa-shaped, one digit off the test number -> Luhn-invalid
	fixtureRealIBAN       = "GB82WEST1234569876" + "5432" // real, checksum-valid example IBAN
	fixtureInvalidIBAN    = "GB82WEST1234569876" + "5433" // one digit off -> checksum-invalid
)

func TestScanPIIFinancialDetectsSSN(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "ssn: "+fixtureValidSSN+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 1 || got[0].Rule != ruleSSN || got[0].Category != "pii" {
		t.Fatalf("got %+v, want one ssn/pii finding", got)
	}
}

func TestScanPIIFinancialRejectsInvalidAreaSSN(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "ssn: "+fixtureInvalidAreaSSN+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings: area 000 is never issued", got)
	}
}

func TestScanPIIFinancialDetectsCreditCard(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "card: "+fixtureRealVisaShape+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 1 || got[0].Rule != ruleCreditCard || got[0].Category != "financial" {
		t.Fatalf("got %+v, want one credit_card_number/financial finding", got)
	}
}

func TestScanPIIFinancialExemptsPublishedTestCardNumbers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "visa: "+fixtureVisaTestCard+"\nmc: "+fixtureMCTestCard+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings: both are the exact, published Luhn-valid test numbers", got)
	}
}

// TestScanPIIFinancialCreditCardChecksumGatesFinding confirms the shape
// alone is never sufficient: a Visa-shaped number one digit off a
// Luhn-valid number is never flagged.
func TestScanPIIFinancialCreditCardChecksumGatesFinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "card: "+fixtureLuhnInvalid+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings: Visa-shaped but Luhn-invalid", got)
	}
}

// TestScanPIIFinancialRealCardAlongsideTestCardStillFlagged pins the
// per-file, per-occurrence semantics: a file that legitimately quotes the
// published test number and also leaks a real-shaped card is still
// reported.
func TestScanPIIFinancialRealCardAlongsideTestCardStillFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "mixed.txt", "test: "+fixtureVisaTestCard+"\nreal: "+fixtureRealVisaShape+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	var sawCard bool
	for _, f := range got {
		if f.Rule == ruleCreditCard {
			sawCard = true
		}
	}
	if !sawCard {
		t.Fatalf("got %+v, want the real-shaped card still caught alongside the exempt test number", got)
	}
}

func TestScanPIIFinancialDetectsIBAN(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "iban: "+fixtureRealIBAN+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 1 || got[0].Rule != ruleIBAN || got[0].Category != "financial" {
		t.Fatalf("got %+v, want one iban/financial finding", got)
	}
}

// TestScanPIIFinancialIBANChecksumGatesFinding confirms the shape alone is
// never sufficient: an IBAN-shaped string one digit off a checksum-valid
// IBAN is never flagged.
func TestScanPIIFinancialIBANChecksumGatesFinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "iban: "+fixtureInvalidIBAN+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings: IBAN-shaped but checksum-invalid", got)
	}
}

func TestScanPIIFinancialCleanFixturePasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "clean.txt", "nothing sensitive here\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings on a clean fixture", got)
	}
}

// TestScanPIIFinancialSkipsExcludedDirs confirms a finding planted under a
// skip-classified directory is never reported.
func TestScanPIIFinancialSkipsExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "node_modules/pkg/leak.txt", fixtureValidSSN+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want skip-ruled directory excluded", got)
	}
}

// TestScanPIIFinancialMixedFileFlagsEveryCategory confirms a single file
// carrying an SSN, a real credit card, and a real IBAN all at once yields
// one finding per category, not just the first one found.
func TestScanPIIFinancialMixedFileFlagsEveryCategory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "mixed.txt", "ssn="+fixtureValidSSN+"\ncard="+fixtureRealVisaShape+"\niban="+fixtureRealIBAN+"\n")

	got, err := ScanPIIFinancial(dir, DefaultSkipRules)
	if err != nil {
		t.Fatalf("ScanPIIFinancial: %v", err)
	}
	rules := map[string]bool{}
	for _, f := range got {
		rules[f.Rule] = true
	}
	for _, want := range []string{ruleSSN, ruleCreditCard, ruleIBAN} {
		if !rules[want] {
			t.Errorf("missing expected rule %s in %+v", want, got)
		}
	}
}
