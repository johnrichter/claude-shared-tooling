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

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
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

	got, err := ScanSecrets(dir, DefaultSkipRules, nil)
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

// TestScanPrivacyPrivateTierAllowsInternalMarkers confirms the loosest tier
// raises neither a forbidden-marker failure nor an internal-identifier
// warning for content the public/confidential tiers would flag.
func TestScanPrivacyPrivateTierAllowsInternalMarkers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\n---\n\nsee host.corp and jane@example.com\n")

	failures, warnings, err := ScanPrivacy(dir, TierPrivate, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(failures) != 0 || len(warnings) != 0 {
		t.Fatalf("got failures=%+v warnings=%+v, want private tier fully permissive", failures, warnings)
	}
}

// TestScanPrivacyForbiddenMarkerTierMatrix pins the whole forbidden-marker
// rule in one place: a tier forbids exactly those privacy values naming a
// tier MORE sensitive than its own, and nothing else. It covers the clauses
// no single-case test reaches - that the public tier still catches the legacy
// "internal" value, that the confidential tier forbids "private" alone (not
// "internal", and not its own "confidential"), and that every alternative is
// word-boundary-anchored, so a value merely prefixed by a forbidden one
// ("privateish", "internally") is never a marker match.
//
// Only forbidden_marker findings are inspected: the public tier's separate
// not_public_pair check fails most of these files for an unrelated reason,
// and would otherwise mask a lost marker alternative.
func TestScanPrivacyForbiddenMarkerTierMatrix(t *testing.T) {
	// forbiddenAt is the set of tiers each value is a forbidden marker for.
	cases := []struct {
		value       string
		forbiddenAt []PrivacyTier
	}{
		{"public", nil},
		{"internal", []PrivacyTier{TierPublic}},
		{"confidential", []PrivacyTier{TierPublic}},
		{"private", []PrivacyTier{TierPublic, TierConfidential}},
		{"PRIVATE", []PrivacyTier{TierPublic, TierConfidential}},
		{"restricted", nil},
		{"privateish", nil},
		{"internally", nil},
		{"confidentially", nil},
	}
	for _, tc := range cases {
		for _, tier := range []PrivacyTier{TierPublic, TierConfidential, TierPrivate} {
			t.Run(string(tier)+"/"+tc.value, func(t *testing.T) {
				dir := t.TempDir()
				writeFile(t, dir, "doc.md", "---\nprivacy: "+tc.value+"\n---\n\nbody\n")

				failures, _, err := ScanPrivacy(dir, tier, PrivacyOptions{SkipRules: DefaultSkipRules})
				if err != nil {
					t.Fatalf("ScanPrivacy: %v", err)
				}
				var markers int
				for _, f := range failures {
					if f.Rule == "forbidden_marker" {
						markers++
					}
				}
				want := 0
				for _, forbidden := range tc.forbiddenAt {
					if forbidden == tier {
						want = 1
					}
				}
				if markers != want {
					t.Fatalf("privacy: %s at tier %s: got %d forbidden_marker findings (%+v), want %d", tc.value, tier, markers, failures, want)
				}
			})
		}
	}
}

// TestScanPrivacyConfidentialTierAllowsInternalHostnameButFlagsPrivateNetwork
// confirms the relaxed confidential-tier internal-id posture: an internal
// hostname is expected in an org-shared repo (no warning), but a
// private-network URL is still flagged.
func TestScanPrivacyConfidentialTierAllowsInternalHostnameButFlagsPrivateNetwork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "see host.corp and http://10.0.0.5/admin\n")

	_, warnings, err := ScanPrivacy(dir, TierConfidential, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 || warnings[0].Rule != "internal_identifier" {
		t.Fatalf("got %+v, want exactly one private-network warning under confidential tier", warnings)
	}
}

// TestScanPrivacyEmployeeEmailFlagsAnyDomainByDefault confirms that with no
// caller-supplied PrivacyOptions.EmployeeEmail, a real, non-example.com
// address is flagged: the check's polarity is allow-list, so an unconfigured
// domain is suspicious by default rather than exempt by default.
func TestScanPrivacyEmployeeEmailFlagsAnyDomainByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@acme-corp.com\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %+v, want one warning for an address at an unallowed domain", warnings)
	}
}

// TestScanPrivacyEmployeeEmailExampleDomainNeverFlags confirms
// user@example.com never flags, even with an empty or nil EmployeeEmail
// config: the RFC 2606 reserved documentation-example domain is always
// allowed, unconditionally.
func TestScanPrivacyEmployeeEmailExampleDomainNeverFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@example.com or Jane@Example.Com\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("got %+v, want no warnings - example.com is always allowed, at any casing", warnings)
	}
}

// TestScanPrivacyEmployeeEmailAllowedDomainConfigDoesNotFlag confirms a
// caller-configured AllowedDomains entry exempts an address at that domain,
// while an address at a different, unconfigured domain still flags.
func TestScanPrivacyEmployeeEmailAllowedDomainConfigDoesNotFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@acme-corp.com or root@other.com\n")

	opts := PrivacyOptions{
		SkipRules:     DefaultSkipRules,
		EmployeeEmail: EmployeeEmailCheck{AllowedDomains: []string{"acme-corp.com"}},
	}
	_, warnings, err := ScanPrivacy(dir, TierPublic, opts)
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %+v, want exactly one warning (root@other.com); jane@acme-corp.com is allowlisted", warnings)
	}
}

// TestScanPrivacyEmployeeEmailAllowedDomainIsCaseInsensitive confirms a
// caller's AllowedDomains entry exempts a domain regardless of casing on
// either side, so casing never silently defeats the caller's own exemption.
func TestScanPrivacyEmployeeEmailAllowedDomainIsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@Acme-Corp.COM or root@other.com\n")

	opts := PrivacyOptions{
		SkipRules:     DefaultSkipRules,
		EmployeeEmail: EmployeeEmailCheck{AllowedDomains: []string{"acme-corp.com"}},
	}
	_, warnings, err := ScanPrivacy(dir, TierPublic, opts)
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %+v, want exactly one warning (root@other.com); jane@Acme-Corp.COM is allowlisted regardless of casing", warnings)
	}
}

// TestScanPrivacyEmployeeEmailNoDomainMatchFlags confirms an address whose
// domain matches neither defaultAllowedEmailDomain nor any caller-configured
// AllowedDomains entry is flagged.
func TestScanPrivacyEmployeeEmailNoDomainMatchFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact root@unrelated.example\n")

	opts := PrivacyOptions{
		SkipRules:     DefaultSkipRules,
		EmployeeEmail: EmployeeEmailCheck{AllowedDomains: []string{"acme-corp.com"}},
	}
	_, warnings, err := ScanPrivacy(dir, TierPublic, opts)
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %+v, want exactly one warning: the address's domain matches no allowed entry", warnings)
	}
}

// TestScanPrivacyEmployeeEmailCheckNeverAppliesAtConfidentialTier confirms the
// employee-email check stays public-tier-only: the confidential tier's
// relaxed posture never grows this check, configured or not.
func TestScanPrivacyEmployeeEmailCheckNeverAppliesAtConfidentialTier(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@acme-corp.com\n")

	_, warnings, err := ScanPrivacy(dir, TierConfidential, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("got %+v, want no warnings: employee-email check never applies at the confidential tier", warnings)
	}
}

// TestScanPrivacyEmployeeEmailBlankAllowedDomainEntryIgnored confirms a blank
// or whitespace-only AllowedDomains entry - the shape a caller gets from
// splitting a trailing-comma config string - is dropped rather than indexed,
// so it never accidentally exempts anything.
func TestScanPrivacyEmployeeEmailBlankAllowedDomainEntryIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@acme-corp.com\n")

	for _, domains := range [][]string{{"acme-corp.com", ""}, {"", "acme-corp.com"}, {"acme-corp.com", "   "}} {
		opts := PrivacyOptions{
			SkipRules:     DefaultSkipRules,
			EmployeeEmail: EmployeeEmailCheck{AllowedDomains: domains},
		}
		_, warnings, err := ScanPrivacy(dir, TierPublic, opts)
		if err != nil {
			t.Fatalf("ScanPrivacy(domains=%q): %v", domains, err)
		}
		if len(warnings) != 0 {
			t.Fatalf("domains=%q: got %+v, want no warnings - acme-corp.com is allowlisted regardless of a blank sibling entry", domains, warnings)
		}
	}
}

// TestScanPrivacyEmployeeEmailNumericTLDIsNotAnAddress confirms the shapes an
// allow-list-polarity check most easily over-flags - a package-version
// specifier and an IPv4-shaped host - are not treated as addresses. Both are
// DNS-label-shaped on the right of the "@", so only the TLD's leading-letter
// requirement excludes them, and both are pervasive in ordinary source and
// documentation: matching them would swamp the real signal.
func TestScanPrivacyEmployeeEmailNumericTLDIsNotAnAddress(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "install foo@1.0.0, pin git-tools@v0.5.0, probe cache@127.0.0.1\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("got %+v, want no warnings - an all-numeric TLD is never a real address", warnings)
	}
}

// TestScanPrivacyEmployeeEmailDigitLeadingHostLabelStillFlags confirms the
// TLD's leading-letter requirement constrains only the last label: a real
// address at a digit-leading host label is still detected.
func TestScanPrivacyEmployeeEmailDigitLeadingHostLabelStillFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@mail.3m.com\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %+v, want one warning - a digit-leading label before the TLD is a real host label", warnings)
	}
}

// TestScanPrivacyEmployeeEmailShortAlphanumericTLDIsNotAnAddress confirms a
// TLD that starts with a letter but mixes in a digit (bar@a1) is not treated
// as an address: the TLD must be letters-only, not merely letter-led.
func TestScanPrivacyEmployeeEmailShortAlphanumericTLDIsNotAnAddress(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "resolve foo@bar.a1\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("got %+v, want no warnings - a letter-plus-digit TLD is never a real address", warnings)
	}
}

// TestScanPrivacyEmployeeEmailOverlongLabelIsNotAnAddress confirms a domain
// label past DNS's 63-character limit is not treated as an address, in the
// last label position as well as before it. The two positions are matched by
// different halves of the pattern, so the last label needs its own case: an
// unbounded TLD would otherwise let an arbitrarily long all-letter label
// through while every earlier label stayed capped.
func TestScanPrivacyEmployeeEmailOverlongLabelIsNotAnAddress(t *testing.T) {
	overlong := strings.Repeat("a", 64)

	for _, domain := range []string{overlong + ".com", "sub." + overlong} {
		dir := t.TempDir()
		writeFile(t, dir, "doc.md", "contact jane@"+domain+"\n")

		_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
		if err != nil {
			t.Fatalf("ScanPrivacy(jane@%s): %v", domain, err)
		}
		if len(warnings) != 0 {
			t.Fatalf("jane@%s: got %+v, want no warnings - a 64-character label exceeds DNS's 63-character limit", domain, warnings)
		}
	}
}

// TestScanPrivacyEmployeeEmailMaximumLengthLabelStillFlags is the accepting
// half of the label-length boundary: 63 characters is legal DNS, so an
// address there is still flagged - again in both label positions. Paired with
// the 64-character rejection above, this is what proves the cap sits exactly
// on DNS's limit rather than near it, and that both positions agree on it.
func TestScanPrivacyEmployeeEmailMaximumLengthLabelStillFlags(t *testing.T) {
	maxLabel := strings.Repeat("a", 63)

	for _, domain := range []string{maxLabel + ".com", "sub." + maxLabel} {
		dir := t.TempDir()
		writeFile(t, dir, "doc.md", "contact jane@"+domain+"\n")

		_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
		if err != nil {
			t.Fatalf("ScanPrivacy(jane@%s): %v", domain, err)
		}
		if len(warnings) != 1 {
			t.Fatalf("jane@%s: got %+v, want one warning - a 63-character label is within DNS's limit", domain, warnings)
		}
	}
}

// TestScanPrivacyEmployeeEmailIDNAndHyphenatedDomainsStillFlag confirms a
// punycode IDN domain and a hyphenated domain still match as real addresses,
// guarding against a future tightening of the pattern that narrows too far.
func TestScanPrivacyEmployeeEmailIDNAndHyphenatedDomainsStillFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "contact jane@sub.xn--80ak6aa92e.com or root@my-company.co\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("got %+v, want two warnings - both a punycode IDN domain and a hyphenated domain are real addresses", warnings)
	}
}

// TestScanPrivacyNoOwnerConceptReachable confirms an "owner:" frontmatter tag
// - forbidden, or declared-but-not-public - is never flagged at any tier:
// this module's privacy-tier checks no longer key on any owner concept.
func TestScanPrivacyNoOwnerConceptReachable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: public\nowner: confidential\n---\n\nbody\n")

	for _, tier := range []PrivacyTier{TierPublic, TierConfidential, TierPrivate} {
		failures, _, err := ScanPrivacy(dir, tier, PrivacyOptions{SkipRules: DefaultSkipRules})
		if err != nil {
			t.Fatalf("ScanPrivacy(%s): %v", tier, err)
		}
		if len(failures) != 0 {
			t.Fatalf("tier %s: got %+v, want no owner-keyed failures", tier, failures)
		}
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

// TestScanPrivacyReservedSentinelHostnameNotFlaggedAsInternal confirms an
// internal-hostname-shaped label immediately followed by an RFC 6761
// reserved sentinel TLD is a documentation/fixture hostname, not a real
// internal address, and so raises no warning. One case per reserved TLD, so
// dropping any single one from the filter's alternation fails a subtest.
func TestScanPrivacyReservedSentinelHostnameNotFlaggedAsInternal(t *testing.T) {
	for _, sentinel := range []string{"foo.internal.test", "bar.internal.example", "baz.internal.localhost", "qux.internal.invalid"} {
		t.Run(sentinel, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "doc.md", "Deploy target: "+sentinel+"\n")

			_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
			if err != nil {
				t.Fatalf("ScanPrivacy: %v", err)
			}
			if len(warnings) != 0 {
				t.Fatalf("got %+v, want no warnings for reserved-sentinel hostname %q", warnings, sentinel)
			}
		})
	}
}

// TestScanPrivacyRealInternalHostnameStillFlaggedAlongsideSentinel confirms
// the reserved-sentinel filter is adjacency-scoped: a genuine internal
// hostname with no reserved TLD immediately after it still flags, even in
// the same file as a sentinel hostname that must not.
func TestScanPrivacyRealInternalHostnameStillFlaggedAlongsideSentinel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "Fixture: foo.internal.test\nReal deploy target: jenkins-01.internal\n")

	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(warnings) != 1 || warnings[0].Rule != "internal_identifier" {
		t.Fatalf("got %+v, want exactly one internal-identifier warning (the real host, not the sentinel)", warnings)
	}
}

// TestScanPrivacyDisguisedSentinelHostnameStillFlagged confirms the sentinel
// filter fires only when the reserved label is the true end of the host: a
// reserved label with more host content after it - whether disguised as a
// further domain label or behind a bogus port - is not a real sentinel and
// must still warn. The reserved example.{com,net,org} second-level domains
// are the inverse case: they are genuine end-of-host sentinels and must not.
func TestScanPrivacyDisguisedSentinelHostnameStillFlagged(t *testing.T) {
	for _, tc := range []struct {
		host string
		want int
	}{
		{"host.corp.test.attacker.io", 1},      // further host label after the sentinel
		{"host.corp.test:8080.attacker.io", 1}, // bogus port, then more host
		{"host.corp.testing", 1},               // reserved label is only a prefix
		{"host.corp.test:8080", 0},             // real port, true end of host
		{"host.corp.example.com", 0},           // reserved second-level domain
		{"host.corp.example.net", 0},
		{"host.corp.example.org", 0},
		{"host.corp.example.co", 1}, // not a reserved second-level domain
	} {
		t.Run(tc.host, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "doc.md", "Deploy target: "+tc.host+"\n")

			_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
			if err != nil {
				t.Fatalf("ScanPrivacy: %v", err)
			}
			if len(warnings) != tc.want {
				t.Fatalf("got %d warnings %+v for %q, want %d", len(warnings), warnings, tc.host, tc.want)
			}
		})
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

// malformedGlob is an unterminated character class, which doublestar (and
// fsx.ClassifyPath) fails to compile — the shared fixture for every
// malformed-exempt-rule test below.
const malformedGlob = "fixtures/[unterminated"

// TestScanPrivacyMalformedMarkerExemptRuleErrors confirms a malformed
// MarkerExemptRules pattern makes ScanPrivacy fail loudly, naming the bad
// pattern, instead of silently exempting every path from the marker check —
// the fail-open outcome fsx.ClassifyPath's own fail-closed-as-match default
// would otherwise produce for this ruleset.
func TestScanPrivacyMalformedMarkerExemptRuleErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\n---\n\nbody\n")
	exempt := []fsx.Rule{{Pattern: malformedGlob, Class: SkipClass}}

	_, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules, MarkerExemptRules: exempt})
	if err == nil {
		t.Fatal("want an error for a malformed MarkerExemptRules pattern, got nil")
	}
	if !strings.Contains(err.Error(), malformedGlob) {
		t.Fatalf("error %q does not name the malformed pattern %q", err, malformedGlob)
	}
	if !strings.Contains(err.Error(), "MarkerExemptRules") {
		t.Fatalf("error %q does not name MarkerExemptRules", err)
	}
}

// TestScanPrivacyMalformedSecretExemptRuleErrors is the same proof for
// SecretExemptRules on ScanPrivacy's own call path.
func TestScanPrivacyMalformedSecretExemptRuleErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", fixtureAWSKey+"\n")
	exempt := []fsx.Rule{{Pattern: malformedGlob, Class: SkipClass}}

	_, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules, SecretExemptRules: exempt})
	if err == nil {
		t.Fatal("want an error for a malformed SecretExemptRules pattern, got nil")
	}
	if !strings.Contains(err.Error(), malformedGlob) {
		t.Fatalf("error %q does not name the malformed pattern %q", err, malformedGlob)
	}
	if !strings.Contains(err.Error(), "SecretExemptRules") {
		t.Fatalf("error %q does not name SecretExemptRules", err)
	}
}

// TestScanSecretsMalformedSecretExemptRuleErrors is the same proof on
// ScanSecrets' own, independent call path into secretExemptRules.
func TestScanSecretsMalformedSecretExemptRuleErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", fixtureAWSKey+"\n")
	exempt := []fsx.Rule{{Pattern: malformedGlob, Class: SkipClass}}

	_, err := ScanSecrets(dir, DefaultSkipRules, exempt)
	if err == nil {
		t.Fatal("want an error for a malformed SecretExemptRules pattern, got nil")
	}
	if !strings.Contains(err.Error(), malformedGlob) {
		t.Fatalf("error %q does not name the malformed pattern %q", err, malformedGlob)
	}
	if !strings.Contains(err.Error(), "SecretExemptRules") {
		t.Fatalf("error %q does not name SecretExemptRules", err)
	}
}

// TestScanPrivacyMalformedSkipRuleDoesNotError is the regression guard for
// the ruleset this change deliberately leaves untouched: SkipRules' existing
// fail-closed-as-skip behavior for a malformed pattern is a separate,
// already-accepted design decision (cautious in that direction, not
// fail-open), and must keep working exactly as before.
func TestScanPrivacyMalformedSkipRuleDoesNotError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\n---\n\nbody\n")
	skip := []fsx.Rule{{Pattern: malformedGlob, Class: SkipClass}}

	if _, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: skip}); err != nil {
		t.Fatalf("ScanPrivacy: want no error for a malformed SkipRules pattern (fail-closed-as-skip is accepted here), got %v", err)
	}
}

// TestScanSecretsMalformedSkipRuleDoesNotError is ScanSecrets' side of the
// same regression guard.
func TestScanSecretsMalformedSkipRuleDoesNotError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", fixtureAWSKey+"\n")
	skip := []fsx.Rule{{Pattern: malformedGlob, Class: SkipClass}}

	if _, err := ScanSecrets(dir, skip, nil); err != nil {
		t.Fatalf("ScanSecrets: want no error for a malformed SkipRules pattern (fail-closed-as-skip is accepted here), got %v", err)
	}
}

// TestScanPrivacyWellFormedExemptRulesStillWork confirms this change adds no
// regression to the already-shipped exemption features: valid
// MarkerExemptRules and SecretExemptRules patterns still exempt exactly the
// paths they name, with no spurious validation error.
func TestScanPrivacyWellFormedExemptRulesStillWork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "fixtures/case.md", "---\nprivacy: confidential\n---\n\n"+fixtureAWSKey+"\n")
	markerExempt := []fsx.Rule{{Pattern: "fixtures/**", Class: SkipClass}}
	secretExempt := []fsx.Rule{{Pattern: "fixtures/**", Class: SkipClass}}

	failures, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{
		SkipRules:         DefaultSkipRules,
		MarkerExemptRules: markerExempt,
		SecretExemptRules: secretExempt,
	})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	if len(failures) != 0 {
		t.Fatalf("got %+v, want both exemptions to still apply to fixtures/case.md", failures)
	}
}

// braceAlternativeMalformedGlob is the malformed pattern a per-path check
// cannot see: doublestar matches the leading "**" alternative and returns
// before it ever parses the unterminated class, so fsx.ClassifyPath reports it
// as a clean, matching rule for every path - i.e. as a tree-wide exemption.
// Only whole-pattern validation rejects it.
const braceAlternativeMalformedGlob = "{**,[bad}"

// TestScanPrivacyBraceAlternativeMalformedExemptRuleErrors guards the hole a
// path-probing validity check leaves open: this pattern exempts every path it
// is applied to while never being reported malformed for any path, so it must
// be rejected on the pattern itself, not on what it matched.
func TestScanPrivacyBraceAlternativeMalformedExemptRuleErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\n---\n\nbody\n")
	exempt := []fsx.Rule{{Pattern: braceAlternativeMalformedGlob, Class: SkipClass}}

	_, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules, MarkerExemptRules: exempt})
	if err == nil {
		t.Fatal("want an error for a malformed brace alternative, got nil (the forbidden marker in doc.md just went unreported)")
	}
	if !strings.Contains(err.Error(), braceAlternativeMalformedGlob) {
		t.Fatalf("error %q does not name the malformed pattern %q", err, braceAlternativeMalformedGlob)
	}
}

// TestScanPrivacyValidatesExemptRulesBeforeWalking proves the validation is
// eager rather than incidental: against a root that cannot be walked at all,
// the malformed-pattern error is what comes back, so no file was read before
// the ruleset was checked.
func TestScanPrivacyValidatesExemptRulesBeforeWalking(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "no-such-tree")
	exempt := []fsx.Rule{{Pattern: malformedGlob, Class: SkipClass}}

	_, _, err := ScanPrivacy(missingRoot, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules, SecretExemptRules: exempt})
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), malformedGlob) || strings.Contains(err.Error(), "walk") {
		t.Fatalf("error %q is not the pre-walk validation error for %q", err, malformedGlob)
	}
}

// TestScanPrivacyMalformedMarkerExemptAmongValidRules confirms one valid
// ruleset never masks a malformed sibling: every exempt ruleset is validated
// up front, and the error names the one that is actually broken.
func TestScanPrivacyMalformedMarkerExemptAmongValidRules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", "---\nprivacy: confidential\n---\n\n"+fixtureAWSKey+"\n")

	_, _, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{
		SkipRules:         DefaultSkipRules,
		MarkerExemptRules: []fsx.Rule{{Pattern: "fixtures/**", Class: SkipClass}, {Pattern: malformedGlob, Class: SkipClass}},
		SecretExemptRules: []fsx.Rule{{Pattern: "fixtures/**", Class: SkipClass}},
	})
	if err == nil {
		t.Fatal("want an error for the malformed MarkerExemptRules entry, got nil")
	}
	if !strings.Contains(err.Error(), "MarkerExemptRules") || !strings.Contains(err.Error(), malformedGlob) {
		t.Fatalf("error %q does not name MarkerExemptRules and %q", err, malformedGlob)
	}
}
