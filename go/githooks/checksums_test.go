package githooks

import "testing"

// TestLuhnValidAcceptsRealCardShapedNumbers confirms the two industry-
// standard published test numbers - fragment-assembled, matching this
// package's convention - pass the Luhn checksum.
func TestLuhnValidAcceptsRealCardShapedNumbers(t *testing.T) {
	visa := "41111111111111" + "11"
	mastercard := "55555555555544" + "44"
	if !luhnValid(visa) {
		t.Errorf("luhnValid(%q) = false, want true", visa)
	}
	if !luhnValid(mastercard) {
		t.Errorf("luhnValid(%q) = false, want true", mastercard)
	}
}

// TestLuhnValidRejectsOneDigitOff confirms a single-digit perturbation of a
// Luhn-valid number fails the checksum - proving the check is not a no-op.
func TestLuhnValidRejectsOneDigitOff(t *testing.T) {
	nearMiss := "41111111111111" + "12" // last digit changed 1->2
	if luhnValid(nearMiss) {
		t.Errorf("luhnValid(%q) = true, want false (one digit off a valid number)", nearMiss)
	}
}

// TestLuhnValidRejectsNonDigitsAndEmpty confirms non-digit input and the
// empty string are never valid.
func TestLuhnValidRejectsNonDigitsAndEmpty(t *testing.T) {
	for _, s := range []string{"", "not-a-card-number", "411111111111111x"} {
		if luhnValid(s) {
			t.Errorf("luhnValid(%q) = true, want false", s)
		}
	}
}

// TestIBANChecksumValidAcceptsRealExamples confirms two well-known, publicly
// documented example IBANs (the UK and German examples used across banking
// documentation) pass the mod-97 checksum.
func TestIBANChecksumValidAcceptsRealExamples(t *testing.T) {
	uk := "GB82WEST12345698765432"
	de := "DE89370400440532013000"
	if !ibanChecksumValid(uk) {
		t.Errorf("ibanChecksumValid(%q) = false, want true", uk)
	}
	if !ibanChecksumValid(de) {
		t.Errorf("ibanChecksumValid(%q) = false, want true", de)
	}
}

// TestIBANChecksumValidRejectsOneDigitOff confirms a single-digit
// perturbation of a checksum-valid IBAN fails - proving big.Int mod-97
// arithmetic, not a shape-only check.
func TestIBANChecksumValidRejectsOneDigitOff(t *testing.T) {
	nearMiss := "GB82WEST12345698765433" // last digit changed 2->3
	if ibanChecksumValid(nearMiss) {
		t.Errorf("ibanChecksumValid(%q) = true, want false (one digit off a valid IBAN)", nearMiss)
	}
}

// TestIBANChecksumValidRejectsShortAndNonAlphanumeric confirms a candidate
// shorter than 4 characters, or containing anything besides A-Z/0-9, is
// never valid.
func TestIBANChecksumValidRejectsShortAndNonAlphanumeric(t *testing.T) {
	for _, s := range []string{"", "GB8", "GB82-WEST-1234"} {
		if ibanChecksumValid(s) {
			t.Errorf("ibanChecksumValid(%q) = true, want false", s)
		}
	}
}

// TestIBANChecksumValidIsCaseInsensitive confirms a lowercase IBAN checksums
// identically to its uppercase form.
func TestIBANChecksumValidIsCaseInsensitive(t *testing.T) {
	if !ibanChecksumValid("gb82west12345698765432") {
		t.Error("ibanChecksumValid on a lowercase real IBAN = false, want true")
	}
}
