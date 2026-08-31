package githooks

import (
	"math/big"
	"strings"
)

// luhnValid reports whether digits (ASCII decimal digits only, no spaces or
// separators) satisfies the Luhn (mod-10) checksum used by every major
// payment-card network. It is the mandatory second gate on top of a
// brand-prefix/length regex match in ScanPIIFinancial's credit-card rule: a
// shape-valid but checksum-invalid number (e.g. 4111111111111112, one digit
// off the well-known Visa test number) is never flagged, cutting the
// false-positive rate a regex alone cannot. An empty string, or one
// containing a non-digit, is never valid.
func luhnValid(digits string) bool {
	if digits == "" {
		return false
	}
	sum := 0
	parity := len(digits) % 2
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if i%2 == parity {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// ibanBase36Digit maps an uppercase letter to its ISO 7064 numeric value
// (A=10 .. Z=35) as a single ASCII byte run, or -1 for anything else.
func ibanBase36Digit(r byte) int {
	if r >= 'A' && r <= 'Z' {
		return int(r-'A') + 10
	}
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	return -1
}

// ibanChecksumValid reports whether s (an IBAN candidate: letters and digits
// only, no spaces, already uppercased) satisfies the ISO 7064 mod-97
// checksum: move the first four characters (country code + two check digits)
// to the end, expand every letter to its two-digit numeric value (A=10..Z=35),
// and confirm the resulting decimal number mod 97 equals 1. That number
// routinely exceeds 30+ digits for a real IBAN - far past int64 range - so
// this uses math/big rather than chunked modular arithmetic. A candidate
// shorter than 4 characters, or containing anything besides A-Z/0-9, is never
// valid.
func ibanChecksumValid(s string) bool {
	s = strings.ToUpper(s)
	if len(s) < 4 {
		return false
	}
	rearranged := s[4:] + s[:4]

	var numeric strings.Builder
	for i := 0; i < len(rearranged); i++ {
		v := ibanBase36Digit(rearranged[i])
		if v < 0 {
			return false
		}
		if v >= 10 {
			numeric.WriteByte(byte('0' + v/10))
			numeric.WriteByte(byte('0' + v%10))
		} else {
			numeric.WriteByte(byte('0' + v))
		}
	}

	n, ok := new(big.Int).SetString(numeric.String(), 10)
	if !ok {
		return false
	}
	mod := new(big.Int).Mod(n, big.NewInt(97))
	return mod.Int64() == 1
}
