package githooks

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// Rule ids for ScanPIIFinancial's three checks - stable, public Finding.Rule
// values, matching the existing package convention (see labelAWSAccessKeyID).
const (
	ruleSSN        = "ssn"
	ruleCreditCard = "credit_card_number"
	ruleIBAN       = "iban"
)

// ssnPattern matches a US Social Security Number in its standard
// AAA-GG-SSSS written form. Validity beyond the shape - area/group/serial
// range rules - is checked separately by ssnPartsValid, since no checksum
// exists for SSNs (per the "secret-scanner rework" plan, §6).
var ssnPattern = regexp.MustCompile(`\b(\d{3})-(\d{2})-(\d{4})\b`)

// ssnPartsValid reports whether an SSN's three components fall within the
// ranges the Social Security Administration has ever actually issued: the
// area is never 000, 666, or 900-999; the group is never 00; the serial is
// never 0000. A structurally SSN-shaped number outside these ranges (e.g.
// 000-12-3456) is provably not a real SSN, so it is never flagged.
func ssnPartsValid(area, group, serial string) bool {
	if area == "000" || area == "666" {
		return false
	}
	if a, err := strconv.Atoi(area); err != nil || a >= 900 {
		return false
	}
	if group == "00" {
		return false
	}
	if serial == "0000" {
		return false
	}
	return true
}

// creditCardPatterns are brand-prefix-and-length shapes for the four major
// networks (per the "secret-scanner rework" plan, §6). Each candidate this
// matches is still gated by luhnValid before it is ever flagged - the shape
// alone is not sufficient, since e.g. 4111111111111112 (one digit off the
// well-known Visa test number) matches the Visa shape but fails Luhn.
var creditCardPatterns = []secretPattern{
	{regexp.MustCompile(`\b4\d{12}(?:\d{3})?\b`), "visa"},
	{regexp.MustCompile(`\b5[1-5]\d{14}\b`), "mastercard"},
	{regexp.MustCompile(`\b3[47]\d{13}\b`), "amex"},
	{regexp.MustCompile(`\b6(?:011|5\d{2})\d{12}\b`), "discover"},
}

// exactCreditCardExemptions holds the two industry-standard Luhn-valid test
// numbers every payment-network test suite uses (Visa's and Mastercard's own
// published test numbers) - no shape/checksum check alone can distinguish
// these from a real card number, so they are exempted by exact value only,
// matching the convention exactExemptions already establishes for secrets.go.
//
// Fragment-assembled for the same reason as awsExampleAccessKeyIDs: this
// source line must never itself contain an unbroken, real-looking financial
// instrument number.
var exactCreditCardExemptions = map[string]bool{
	"41111111111111" + "11": true, // Visa's published test number
	"55555555555544" + "44": true, // Mastercard's published test number
}

// ibanPattern matches an IBAN's structural shape: a two-letter country code,
// two check digits, then up to 30 further alphanumeric characters (ISO
// 13616's own maximum total length is 34). ibanChecksumValid (mod-97, see
// checksums.go) is the mandatory second gate - the shape alone matches far
// too much incidental alphanumeric text to be flagged on its own.
var ibanPattern = regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`)

// fileHasValidSSN reports whether text contains at least one SSN-shaped
// match whose area/group/serial fall within an ever-issued range.
func fileHasValidSSN(text string) bool {
	for _, m := range ssnPattern.FindAllStringSubmatch(text, -1) {
		if ssnPartsValid(m[1], m[2], m[3]) {
			return true
		}
	}
	return false
}

// fileHasValidCreditCard reports whether text contains at least one
// brand-shaped, Luhn-valid, non-exempt credit card number - mirroring
// matchesSecretPattern's semantics in secrets.go (one finding per file per
// rule, not per occurrence; a file is flagged if ANY occurrence is
// non-exempt, even alongside an exempt one).
func fileHasValidCreditCard(text string) bool {
	for _, p := range creditCardPatterns {
		for _, match := range p.re.FindAllString(text, -1) {
			if !exactCreditCardExemptions[match] && luhnValid(match) {
				return true
			}
		}
	}
	return false
}

// fileHasValidIBAN reports whether text contains at least one
// checksum-valid IBAN.
func fileHasValidIBAN(text string) bool {
	for _, match := range ibanPattern.FindAllString(text, -1) {
		if ibanChecksumValid(match) {
			return true
		}
	}
	return false
}

// ScanPIIFinancial walks root and reports every file containing a
// structurally valid, checksum-confirmed (where a checksum exists) SSN,
// credit card number, or IBAN - the two hand-rolled categories betterleaks
// itself does not cover at all (per the "secret-scanner rework" plan, §6:
// betterleaks' real 461-rule default config carries zero PII or raw-
// financial-instrument rules). Every finding is tagged "pii" (SSN) or
// "financial" (credit card, IBAN).
//
// Unlike ScanSecrets/ScanPrivacy's secret check, there is no separate
// exempt-by-path ruleset here beyond skipRules: this is a small, standalone
// pass with no caller-configurable exemption surface, matching the plan's
// "no way for end users to change our default set of rules" posture for
// every category in this package.
//
// SSN has no checksum (none exists), so ssnPartsValid's area/group/serial
// range check is its only validity gate; a near-miss just outside those
// ranges is provably not a real SSN and is never flagged. Credit card and
// IBAN are both checksum-gated (luhnValid, ibanChecksumValid); a
// shape-matching but checksum-invalid number is never flagged. Two
// industry-standard Luhn-valid test card numbers (see
// exactCreditCardExemptions) are exempted by exact value, since no
// shape/checksum check alone can distinguish them from a real card number.
func ScanPIIFinancial(root string, skipRules []fsx.Rule) ([]Finding, error) {
	var findings []Finding
	err := walkScannable(root, skipRules, func(rel, abs string) error {
		if binarySuffixes[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil // unreadable — nothing to leak in text form
		}
		if !isValidUTF8(data) {
			return nil
		}
		text := string(data)

		if fileHasValidSSN(text) {
			findings = append(findings, Finding{Path: rel, Rule: ruleSSN, Detail: "possible US Social Security Number", Category: "pii"})
		}
		if fileHasValidCreditCard(text) {
			findings = append(findings, Finding{Path: rel, Rule: ruleCreditCard, Detail: "possible credit card number", Category: "financial"})
		}
		if fileHasValidIBAN(text) {
			findings = append(findings, Finding{Path: rel, Rule: ruleIBAN, Detail: "possible IBAN", Category: "financial"})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}
