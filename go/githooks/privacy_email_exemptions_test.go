package githooks

import (
	"math/rand"
	"testing"
)

// scanEmployeeWarnings runs the public-tier privacy scan over a single-file
// tree and returns the warning count, the sole surface every employee-email
// exemption test below asserts against.
func scanEmployeeWarnings(t *testing.T, content string) int {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "doc.md", content)
	_, warnings, err := ScanPrivacy(dir, TierPublic, PrivacyOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanPrivacy: %v", err)
	}
	return len(warnings)
}

// TestScanPrivacyEmployeeEmailReservedDomainAddressReportsNoHit confirms the
// reserved-domain false-alarm shape: an address at a subdomain of an RFC 2606
// documentation domain is a placeholder, not an internal identifier, and is
// exempted in the rule's own default set with no caller configuring it.
func TestScanPrivacyEmployeeEmailReservedDomainAddressReportsNoHit(t *testing.T) {
	if n := scanEmployeeWarnings(t, "export TARGET=user@remote.example.com\n"); n != 0 {
		t.Fatalf("got %d warnings, want 0 - user@remote.example.com is a reserved documentation domain", n)
	}
}

// TestScanPrivacyEmployeeEmailPublishedSharedAliasReportsNoHit confirms the
// published-shared-alias false-alarm shape: the organization's own public role
// address is exempted by exact match in the rule's own default set, with no
// caller configuring it.
func TestScanPrivacyEmployeeEmailPublishedSharedAliasReportsNoHit(t *testing.T) {
	if n := scanEmployeeWarnings(t, "Questions? Email support@datadoghq.com for help.\n"); n != 0 {
		t.Fatalf("got %d warnings, want 0 - support@datadoghq.com is a published shared alias", n)
	}
}

// TestScanPrivacyEmployeeEmailSSHRemoteStringReportsNoHit confirms the
// SSH-remote-string false-alarm shape: a git clone URL, in both scp-like and
// git+ssh:// forms, carries the git service-user prefix and is exempted rather
// than reported as an employee address.
func TestScanPrivacyEmployeeEmailSSHRemoteStringReportsNoHit(t *testing.T) {
	for _, remote := range []string{
		"git clone git@github.com:johnrichter/claude-marketplace.git",
		"dep = git+ssh://git@nonreserved-host.com/org/shared-lib@v1.2.0",
	} {
		if n := scanEmployeeWarnings(t, remote+"\n"); n != 0 {
			t.Fatalf("got %d warnings, want 0 - %q is an SSH remote string, not an employee address", n, remote)
		}
	}
}

// TestScanPrivacyEmployeeEmailRealAddressStillReportsHit is the accepting case
// the exemptions must never swallow: a genuine employee-shaped address at a
// real, non-reserved domain still reports a hit.
func TestScanPrivacyEmployeeEmailRealAddressStillReportsHit(t *testing.T) {
	if n := scanEmployeeWarnings(t, "reviewer: alex.morgan@datadoghq.com\n"); n != 1 {
		t.Fatalf("got %d warnings, want 1 - alex.morgan@datadoghq.com is a real employee-shaped address", n)
	}
}

// TestScanPrivacyEmployeeEmailSSHRemoteExemptButProseAddressStillHits pins the
// SSH exemption's boundary directly: in one file an SSH remote string reports
// no hit while an employee-shaped address in prose still does, so the exemption
// covers the remote shape without silencing a real address beside it.
func TestScanPrivacyEmployeeEmailSSHRemoteExemptButProseAddressStillHits(t *testing.T) {
	content := "Clone with git@github.com:org/repo.git, then email alex.morgan@datadoghq.com.\n"
	if n := scanEmployeeWarnings(t, content); n != 1 {
		t.Fatalf("got %d warnings, want exactly 1 (the prose address, not the SSH remote)", n)
	}
}

// TestScanPrivacyEmployeeEmailReservedDomainBoundary is the boundary between an
// exempt reserved domain and a lookalike just outside the reserved set. A
// lookalike must still report a hit, so the reserved-domain exemption cannot
// fail open by matching more than the reserved set names.
func TestScanPrivacyEmployeeEmailReservedDomainBoundary(t *testing.T) {
	for _, tc := range []struct {
		domain string
		want   int
	}{
		{"example.com", 0},         // RFC 2606 documentation domain
		{"sub.example.com", 0},     // subdomain of it
		{"x.example.net", 0},       // .net sibling
		{"build.ci.test", 0},       // RFC 6761 reserved TLD
		{"thing.invalid", 0},       // reserved TLD
		{"node.localhost", 0},      // reserved TLD
		{"notexample.com", 1},      // lookalike: reserved label is not a whole label
		{"example.company.com", 1}, // lookalike: real domain company.com
		{"mytest.com", 1},          // lookalike: "test" is not the TLD
		{"testbed.io", 1},          // lookalike: "test" is not the TLD
		{"datadoghq.com", 1},       // a real, non-reserved domain
	} {
		t.Run(tc.domain, func(t *testing.T) {
			if n := scanEmployeeWarnings(t, "contact user@"+tc.domain+"\n"); n != tc.want {
				t.Fatalf("domain %q: got %d warnings, want %d", tc.domain, n, tc.want)
			}
		})
	}
}

// TestIsSSHRemoteBoundary pins the SSH exemption's declared set: the match's
// local part must be exactly the git service user. A lookalike local part -
// one that merely contains or resembles "git" - is outside the set and is not
// exempted.
func TestIsSSHRemoteBoundary(t *testing.T) {
	for _, tc := range []struct {
		match string
		want  bool
	}{
		{"git@github.com", true},
		{"GIT@github.com", true}, // service user is matched case-insensitively
		{"git@nonreserved-host.com", true},
		{"gitlab@github.com", false},
		{"legit@github.com", false},
		{"digit@github.com", false},
		{"alex.morgan@datadoghq.com", false},
	} {
		if got := isSSHRemote(tc.match); got != tc.want {
			t.Fatalf("isSSHRemote(%q) = %v, want %v", tc.match, got, tc.want)
		}
	}
}

// TestEmployeeEmailExemptNeverMatchesAddressOutsideDeclaredSet is the property
// test: an address whose local part is not the git service user and whose
// domain is neither reserved nor a published alias nor a caller entry is
// outside every declared exemption set, and must never be exempted - the
// control must not fail open on a randomly shaped real address.
func TestEmployeeEmailExemptNeverMatchesAddressOutsideDeclaredSet(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const letters = "abcdefghijklmnopqrstuvwxyz"
	randLabel := func(minLen int) string {
		b := make([]byte, minLen+rng.Intn(6))
		for i := range b {
			b[i] = letters[rng.Intn(len(letters))]
		}
		return string(b)
	}
	// A pool of real, non-reserved TLDs: none is an RFC 6761/2606 reserved name.
	nonReservedTLDs := []string{"com", "net", "org", "io", "dev", "co", "app"}
	noCallerAllowList := map[string]bool{}

	for i := 0; i < 4000; i++ {
		local := randLabel(2)
		if local == sshRemoteUser {
			continue // this one IS inside the SSH set; skip it
		}
		label := randLabel(2)
		if label == "example" {
			continue // "example.com" etc. would be inside the reserved set
		}
		domain := label + "." + nonReservedTLDs[rng.Intn(len(nonReservedTLDs))]
		addr := local + "@" + domain
		if employeeEmailExempt(addr, noCallerAllowList) {
			t.Fatalf("addr %q is outside every declared exemption set but was exempted", addr)
		}
	}
}

// TestScanPrivacyBaselineFalseAlarmsAllExempt reproduces the four employee-email
// hits a public repository's privacy scan reported before this widening - two
// SSH remote strings, one reserved-domain placeholder, and one published shared
// alias - and confirms each now falls under one of the three false-alarm shapes,
// so the rule reports none of them. Its planted counter-probe - a real
// employee-shaped address at the same domain as the shared alias - must still
// report a hit, so the exemptions did not silence the instrument.
func TestScanPrivacyBaselineFalseAlarmsAllExempt(t *testing.T) {
	baseline := "" +
		"git clone git@github.com:johnrichter/claude-marketplace.git\n" + // SSH remote string
		"dep = git+ssh://git@github.example.com/org/shared-lib@v1.2.0\n" + // SSH remote string (host also reserved)
		"export TARGET=user@remote.example.com\n" + // reserved domain
		"Questions? support@datadoghq.com\n" // published shared alias
	if n := scanEmployeeWarnings(t, baseline); n != 0 {
		t.Fatalf("got %d warnings, want 0 - every recorded baseline hit is a false alarm now exempted", n)
	}

	withCounterProbe := baseline + "Contact: alex.morgan@datadoghq.com for details.\n"
	if n := scanEmployeeWarnings(t, withCounterProbe); n != 1 {
		t.Fatalf("got %d warnings, want exactly 1 - the counter-probe real address must still report a hit", n)
	}
}
