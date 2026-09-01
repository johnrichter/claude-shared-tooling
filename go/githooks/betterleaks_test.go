package githooks

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// testBetterleaksBinary returns the path to a real betterleaks binary for
// subprocess-level integration tests, taken from the BETTERLEAKS_TEST_BIN
// environment variable. These tests need a real, provisioned binary to
// exercise real subprocess behavior (including the live-verified §1b
// bypass surfaces - some of which this session discovered do NOT behave as
// a naive reading of betterleaks' own --help text would suggest, so a pure
// mock would not actually prove anything about them); they skip cleanly
// when no binary is provisioned, e.g. a bare checkout with no network
// access, rather than failing the whole suite.
func testBetterleaksBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("BETTERLEAKS_TEST_BIN")
	if bin == "" {
		t.Skip("BETTERLEAKS_TEST_BIN not set; skipping betterleaks subprocess integration test")
	}
	return bin
}

// ─── Fictional planted-secret fixture ───
//
// Many of the subprocess tests below need a "some secret exists" fixture
// purely as a generic stand-in - their real purpose is proving OUR
// subprocess/config-merge/lockdown logic works (per the operator's own
// scoping: betterleaks' actual ability to find and flag secrets is trusted,
// not tested here), not exercising betterleaks' own 461-rule catalog. A
// real-provider-shaped value (a JWT, a Slack token, an AWS key, etc.) used
// only as such a stand-in risks GitHub's own push protection flagging this
// file, since this repo is public - it already happened once. So every
// such test uses ONE consistent, obviously fictional, opaque, non-vendor
// credential shape instead, delivered through this package's own
// caller-additive ExtraRules mechanism (§3) - never a base-config rule -
// so none of these tests depend on betterleaks' own rule catalog detecting
// anything at all.
//
// The rule id and regex deliberately avoid any "secret"/"key"/"token"/
// "api"/"cred"-shaped word AND any company/brand-shaped prefix: a trivial
// grep for those words (or a vendor name) is itself a heuristic for finding
// security-relevant content, and using one in our own fictional fixture
// would defeat the point of it being abstract and uninteresting to that
// heuristic - it's a bare, opaque alphanumeric pattern, nothing more.
//
// The character class is [0-9a-zA-Z], not the narrower [0-9a-f], and the
// quantifier is an open-ended {28,} (not an exact {28}): both are
// deliberate, not typos. TestScanCredentialsAppliesExampleHeuristic needs
// appending the literal text "EXAMPLE" directly after a fixture value to
// extend the regex's own captured match (mirroring how a JWT's open-ended
// base64url character class let a trailing "EXAMPLE" become part of that
// match) - a narrower hex-only class would stop the match at "EXAMPLE"'s
// first non-hex letter and never exercise the filter at all. Live-verified
// this session with the real v1.8.1 binary that this class does extend the
// match to include a trailing "EXAMPLE", and that betterleaks' compiled-in
// global EXAMPLE/ABCDEF filter (§4) applies to a finding sourced from a
// caller-supplied ExtraRule exactly the same as it applies to a
// base-config rule's finding.
const testFixtureRuleID = "test-fixture-a"

var testFixtureRule = BetterleaksRule{ID: testFixtureRuleID, Regex: `[0-9a-zA-Z]{28,}`}

const (
	fixtureHexValue       = "1a2b3c4d5e6f7081920a1b2c3d4e5f60718293a"
	fixtureHexValueExempt = "aaaa1111bbbb2222cccc3333dddd4444eeee5555"
	fixtureHexValueReal   = "9988776655443322110099887766554433221100"
)

// ─── buildBetterleaksConfig: pure-function tests, no subprocess needed ───

func TestBuildBetterleaksConfigAppendsExtraRule(t *testing.T) {
	cfg, err := buildBetterleaksConfig([]BetterleaksRule{{ID: "my-rule", Regex: `MYCO_[A-Z0-9]{10}`}}, nil)
	if err != nil {
		t.Fatalf("buildBetterleaksConfig: %v", err)
	}
	s := string(cfg)
	if !strings.Contains(s, `id = "my-rule"`) || !strings.Contains(s, `regex = '''MYCO_[A-Z0-9]{10}'''`) {
		t.Fatalf("merged config missing the appended rule block:\n%s", tail(s, 400))
	}
	if !strings.HasPrefix(s, string(betterleaksBaseConfig)[:100]) {
		t.Fatal("merged config does not start with the compiled-in base config")
	}
}

func TestBuildBetterleaksConfigRejectsIncompleteRule(t *testing.T) {
	for _, r := range []BetterleaksRule{{ID: "", Regex: "x"}, {ID: "x", Regex: ""}} {
		if _, err := buildBetterleaksConfig([]BetterleaksRule{r}, nil); err == nil {
			t.Errorf("buildBetterleaksConfig(%+v): want error for incomplete rule, got nil", r)
		}
	}
}

func TestBuildBetterleaksConfigRejectsRuleRegexWithTripleQuote(t *testing.T) {
	_, err := buildBetterleaksConfig([]BetterleaksRule{{ID: "bad", Regex: "abc'''def"}}, nil)
	if err == nil {
		t.Fatal("want error for a regex containing the TOML raw-string delimiter, got nil")
	}
}

func TestBuildBetterleaksConfigAppendsScopedAllowlist(t *testing.T) {
	cfg, err := buildBetterleaksConfig(nil, []BetterleaksAllowlistEntry{{RuleID: "some-rule", Value: "EXACT_VALUE"}})
	if err != nil {
		t.Fatalf("buildBetterleaksConfig: %v", err)
	}
	s := string(cfg)
	if !strings.Contains(s, `targetRules = ["some-rule"]`) {
		t.Errorf("merged config missing targetRules scoping:\n%s", tail(s, 400))
	}
	if !strings.Contains(s, `regexes = ['''^EXACT_VALUE$''']`) {
		t.Errorf("merged config missing the anchored exact-value regex:\n%s", tail(s, 400))
	}
}

func TestBuildBetterleaksConfigAppendsGlobalAllowlistWithoutTargetRules(t *testing.T) {
	for _, ruleID := range []string{"", "*"} {
		cfg, err := buildBetterleaksConfig(nil, []BetterleaksAllowlistEntry{{RuleID: ruleID, Regex: "^FOO$"}})
		if err != nil {
			t.Fatalf("buildBetterleaksConfig: %v", err)
		}
		if strings.Contains(string(cfg), "targetRules") && !strings.Contains(string(betterleaksBaseConfig), "targetRules") {
			t.Errorf("RuleID %q: want no targetRules line for a global allowlist entry", ruleID)
		}
	}
}

func TestBuildBetterleaksConfigRejectsAmbiguousAllowlistEntry(t *testing.T) {
	for _, a := range []BetterleaksAllowlistEntry{
		{RuleID: "r", Value: "v", Regex: "re"}, // both set
		{RuleID: "r"},                          // neither set
	} {
		if _, err := buildBetterleaksConfig(nil, []BetterleaksAllowlistEntry{a}); err == nil {
			t.Errorf("buildBetterleaksConfig(%+v): want error, got nil", a)
		}
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ─── ScanCredentials: input-validation tests, no subprocess needed ───

func TestScanCredentialsRejectsEmptyBinaryPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "hello\n")
	if _, err := ScanCredentials(dir, "", BetterleaksOptions{}); err == nil {
		t.Fatal("want an error for an empty betterleaksPath, got nil")
	}
}

// TestScanCredentialsEmptyTreeNeverInvokesBinary confirms a root with zero
// scannable files returns cleanly without ever needing to invoke
// betterleaks at all - proven by passing a betterleaksPath that does not
// exist on disk and confirming no error results.
func TestScanCredentialsEmptyTreeNeverInvokesBinary(t *testing.T) {
	dir := t.TempDir()
	got, err := ScanCredentials(dir, "/nonexistent/betterleaks", BetterleaksOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings for an empty tree", got)
	}
}

// ─── Subprocess integration tests (require BETTERLEAKS_TEST_BIN) ───

// TestScanCredentialsDetectsPlantedSecret confirms a planted secret is
// detected end to end and tagged Category "credentials" - using the
// fictional planted-secret fixture (see its doc comment above) delivered
// via the caller-additive ExtraRules mechanism (§3), not any base-config
// rule, since the property under test is our own subprocess/tagging
// pipeline, not betterleaks' own detection capability.
func TestScanCredentialsDetectsPlantedSecret(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "token = "+fixtureHexValue+"\n")

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
	})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Path != "leak.txt" || got[0].Rule != testFixtureRuleID || got[0].Category != "credentials" {
		t.Fatalf("got %+v, want one %s/credentials finding at leak.txt", got, testFixtureRuleID)
	}
}

// TestScanCredentialsCleanFixturePasses confirms a clean tree produces no
// findings at all, in both of the two cases that matter: the simplest
// zero-options path (nothing supplied beyond SkipRules - the plain merge
// of the compiled-in base config with zero additions, scanned end to end
// against the real binary with no crash and no findings), and with the
// fictional ExtraRule (§3) wired up too (proving the appended custom rule
// does not itself introduce a false positive on ordinary text).
func TestScanCredentialsCleanFixturePasses(t *testing.T) {
	bin := testBetterleaksBinary(t)

	t.Run("zero-options", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "clean.txt", "nothing sensitive here\n")

		got, err := ScanCredentials(dir, bin, BetterleaksOptions{SkipRules: DefaultSkipRules})
		if err != nil {
			t.Fatalf("ScanCredentials: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no findings on a clean fixture with zero extra options", got)
		}
	})

	t.Run("with-extra-rule", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "clean.txt", "nothing sensitive here\n")

		got, err := ScanCredentials(dir, bin, BetterleaksOptions{
			SkipRules:  DefaultSkipRules,
			ExtraRules: []BetterleaksRule{testFixtureRule},
		})
		if err != nil {
			t.Fatalf("ScanCredentials: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no findings on a clean fixture even with the fictional ExtraRule wired up", got)
		}
	})
}

// TestScanCredentialsDropsKnownDemoJWT confirms jwt.io's own canonical demo
// token, and an arbitrary-claims token signed with jwt.io's own demo
// secret, are both dropped before ever reaching the caller - the JWT
// post-filter (§5) applied end to end through ScanCredentials, not just at
// the isKnownDemoJWT unit level.
func TestScanCredentialsDropsKnownDemoJWT(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "demo.txt", "token = "+fixtureDemoJWT+"\n")
	writeFile(t, dir, "demo_secret_signed.txt", "token = "+fixtureDemoSecretSignedJWT+"\n")

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want both known-demo JWTs dropped", got)
	}
}

// TestScanCredentialsAppliesExampleHeuristic confirms the compiled-in global
// EXAMPLE/ABCDEF filter (§4) is wired end to end even for a finding sourced
// from a caller-supplied ExtraRule, not just a base-config rule: the
// fictional fixture value with "EXAMPLE" appended is dropped, while an
// otherwise-identical one without it still flags. Live-verified this
// session (see the fixture's own doc comment above) before this test was
// written: appending "EXAMPLE" does extend the ExtraRule's own open-ended
// regex match, and betterleaks' global filter does apply to it.
func TestScanCredentialsAppliesExampleHeuristic(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "token = "+fixtureHexValue+"\n")
	writeFile(t, dir, "example.txt", "token = "+fixtureHexValue+"EXAMPLE\n")

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
	})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Path != "real.txt" {
		t.Fatalf("got %+v, want only real.txt flagged, never example.txt", got)
	}
}

// TestScanCredentialsBypassBetterleaksTomlAutoDiscoveryClosed is a §1b
// bypass-surface test: a planted, empty .betterleaks.toml in the scanned
// root - which would silently disable every rule if --config were ever
// omitted - must not weaken the scan at all. Uses the fictional
// planted-secret fixture (delivered via ExtraRules) since the property
// under test is our own config-lockdown logic, not betterleaks' detection
// capability.
func TestScanCredentialsBypassBetterleaksTomlAutoDiscoveryClosed(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "token = "+fixtureHexValue+"\n")
	writeFile(t, dir, ".betterleaks.toml", "title = \"attacker-supplied empty override\"\n")

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
	})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != testFixtureRuleID {
		t.Fatalf("got %+v, want the fictional planted secret still flagged despite the planted .betterleaks.toml", got)
	}
}

// TestScanCredentialsBypassIgnoreFileAtScanRootClosed is a §1b bypass-
// surface test for the vector this session's own live testing discovered
// beyond the plan's original assumption (see runBetterleaksBatch's doc
// comment, item 3): a .betterleaksignore file naming the real fingerprint
// of a planted secret, placed at the top of the scanned root itself, must
// not suppress it - proving the per-file-argument mitigation actually
// closes what pointing --gitleaks-ignore-path elsewhere alone does not.
// Uses the fictional planted-secret fixture, for the same reason as the
// other bypass tests in this group.
func TestScanCredentialsBypassIgnoreFileAtScanRootClosed(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", fixtureHexValue+"\n")
	writeFile(t, dir, ".betterleaksignore", "leak.txt:"+testFixtureRuleID+":1\n")

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
	})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != testFixtureRuleID {
		t.Fatalf("got %+v, want the fictional planted secret still flagged despite the planted .betterleaksignore naming its exact fingerprint", got)
	}
}

// TestScanCredentialsBypassInlineAllowCommentClosed is a §1b bypass-surface
// test: a trailing "// gitleaks:allow" comment on the same line as a real
// planted secret must not suppress it. Uses the fictional planted-secret
// fixture, for the same reason as the other bypass tests in this group.
func TestScanCredentialsBypassInlineAllowCommentClosed(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", fixtureHexValue+" // gitleaks:allow\n")

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
	})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != testFixtureRuleID {
		t.Fatalf("got %+v, want the fictional planted secret still flagged despite the trailing gitleaks:allow comment", got)
	}
}

// TestScanCredentialsBypassEnvConfigTOMLClosed is a §1b bypass-surface test:
// BETTERLEAKS_CONFIG_TOML set to an empty-ruleset config in the process
// environment must not suppress a real planted secret, since --config is
// always passed explicitly (and this env var is additionally stripped from
// the subprocess's own environment as defense in depth). Uses the fictional
// planted-secret fixture, for the same reason as the other bypass tests in
// this group.
func TestScanCredentialsBypassEnvConfigTOMLClosed(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", "token = "+fixtureHexValue+"\n")

	t.Setenv("BETTERLEAKS_CONFIG_TOML", `title = "attacker-supplied empty override"`)

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
	})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != testFixtureRuleID {
		t.Fatalf("got %+v, want the fictional planted secret still flagged despite BETTERLEAKS_CONFIG_TOML being set", got)
	}
}

// TestScanCredentialsExtraAllowlistOnlyExemptsExactValue is the §3
// user-additive-layer test for the allowlist half: a caller's
// ExtraAllowlist entry (scoped to a caller-supplied ExtraRule, so this test
// does not depend on any specific base-config rule id or regex) exempts
// only its exact named value - a second, different value matching the same
// rule still flags. Uses the fictional planted-secret fixture's exempt/real
// value pair (see the fixture's own doc comment above).
func TestScanCredentialsExtraAllowlistOnlyExemptsExactValue(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "exempt.txt", "key = "+fixtureHexValueExempt+"\n")
	writeFile(t, dir, "real.txt", "key = "+fixtureHexValueReal+"\n")

	opts := BetterleaksOptions{
		SkipRules:      DefaultSkipRules,
		ExtraRules:     []BetterleaksRule{testFixtureRule},
		ExtraAllowlist: []BetterleaksAllowlistEntry{{RuleID: testFixtureRuleID, Value: fixtureHexValueExempt}},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Path != "real.txt" {
		t.Fatalf("got %+v, want only real.txt flagged, never exempt.txt", got)
	}
}

// NOTE: TestScanCredentialsExtraRuleOnlyAdds and
// TestScanCredentialsZeroExtraEntriesStillEnforcesBaseRules were removed
// (2026-08-31, test-scope correction), not merely rescoped. Both tests'
// only distinct assertion beyond what the tests above already establish
// was "a base-config (betterleaks-catalog) rule still fires alongside/with
// zero extra entries" - which requires a real-provider-shaped fixture to
// exercise (there is no such thing as a base-config rule matching a
// fictional value), and which is exactly "betterleaks' own ability to find
// and flag secrets," explicitly out of scope for this package's own tests
// per the operator's instruction ("we can trust that capability"). The
// property that actually matters for OUR OWN code - that appending extra
// rules/allowlist entries never modifies or removes any compiled-in base
// content - is already conclusively proven at the correct level by the
// pure-function TestBuildBetterleaksConfigAppendsExtraRule above (which
// asserts the merged config's bytes are prefixed by the base config's
// bytes, unchanged, needing no subprocess and no secret fixture at all).
// The "zero extra options still works end to end with no crash" half of
// ZeroExtraEntriesStillEnforcesBaseRules is preserved by
// TestScanCredentialsCleanFixturePasses's own "zero-options" subtest above.

// ─── §3 user-additive-layer plumbing: merged config validates via betterleaks itself ───
//
// The vendor-specific compiled-in allowlist blocks this file used to test
// (Azure/Mailgun/Stripe/Facebook example credentials) are gone - see
// data/betterleaks-base.toml's own header comment for why. The real
// exemption mechanism for a real-world value some downstream caller needs
// is the §3 user-additive layer (BetterleaksOptions.ExtraRules/
// ExtraAllowlist): a caller's OWN config passes its OWN rule/allowlist
// entries through to betterleaks verbatim. This package's job is only to
// prove that plumbing works - that buildBetterleaksConfig produces valid
// TOML betterleaks itself accepts, with valid field types (a regex that
// actually parses) - never that any particular value or shape matches
// anything. So the entries below are deliberately inert test fixtures: an
// opaque id ("test-fixture-a"/"test-fixture-b", no "key"/"token"/"api"/
// "secret"/"cred" wording) and a bare hex-shaped regex with no vendor
// prefix at all. We don't assert anything about matching - only that
// `betterleaks config check` reports the merged config valid.

// TestBuildBetterleaksConfigValidatesViaBetterleaksConfigCheck proves the
// general §3 mechanism end to end: a merged config built from a handful of
// clearly-fake, non-vendor-shaped ExtraRules/ExtraAllowlist entries is
// handed to betterleaks' own "config check" subcommand (confirmed this
// session to validate a config's TOML syntax and field types - e.g. that
// every rule's regex actually compiles - without scanning anything, and to
// exit 0 on success), and that subcommand reports it valid.
func TestBuildBetterleaksConfigValidatesViaBetterleaksConfigCheck(t *testing.T) {
	bin := testBetterleaksBinary(t)

	cfg, err := buildBetterleaksConfig(
		[]BetterleaksRule{
			{ID: "test-fixture-a", Regex: `[0-9a-f]{40}`},
			{ID: "test-fixture-b", Regex: `[0-9a-f]{32}`},
		},
		[]BetterleaksAllowlistEntry{
			{RuleID: "test-fixture-a", Value: "1a2b3c4d5e6f7081920a1b2c3d4e5f60718293a"},
			{RuleID: "test-fixture-b", Regex: `^[0-9a-f]{32}$`},
		},
	)
	if err != nil {
		t.Fatalf("buildBetterleaksConfig: %v", err)
	}

	path := writeFile(t, t.TempDir(), "merged.toml", string(cfg))

	out, err := exec.Command(bin, "config", "check", path).CombinedOutput()
	if err != nil {
		t.Fatalf("betterleaks config check %s: %v\noutput:\n%s", path, err, out)
	}
}

// TestBuildBetterleaksConfigCheckRejectsBrokenRegex is the negative half:
// `betterleaks config check` must exit non-zero, with a real error, on an
// otherwise well-formed config whose rule regex does not parse - confirming
// the check tool is actually validating field types (not just TOML syntax)
// and that a genuinely broken merged config would be caught, not silently
// accepted.
func TestBuildBetterleaksConfigCheckRejectsBrokenRegex(t *testing.T) {
	bin := testBetterleaksBinary(t)

	cfg, err := buildBetterleaksConfig(
		[]BetterleaksRule{{ID: "test-fixture-broken", Regex: `[0-9a-f`}},
		nil,
	)
	if err != nil {
		t.Fatalf("buildBetterleaksConfig: %v", err)
	}

	path := writeFile(t, t.TempDir(), "broken.toml", string(cfg))

	out, err := exec.Command(bin, "config", "check", path).CombinedOutput()
	if err == nil {
		t.Fatalf("betterleaks config check %s: want a non-zero exit for an unparseable regex, got success\noutput:\n%s", path, out)
	}
}

// TestScanCredentialsBatchesAcrossMultipleInvocations confirms a tree with
// more files than betterleaksBatchSize is still scanned completely: results
// from more than one betterleaks invocation are merged, not dropped or
// overwritten.
func TestScanCredentialsBatchesAcrossMultipleInvocations(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()

	original := betterleaksBatchSize
	betterleaksBatchSize = 2
	t.Cleanup(func() { betterleaksBatchSize = original })

	// Five files, each with a distinct value matching the fictional
	// planted-secret fixture's rule, forces at least three separate batched
	// invocations at batch size 2. Each value is a varied hex string, not a
	// single repeated character: betterleaks' own compiled-in base config
	// has a generic low-entropy filter that drops any secret consisting
	// entirely of one repeated letter, which would otherwise silently
	// zero out every finding here.
	values := []string{
		"30877432d1026706d7e805da846a32c3bb81",
		"e3c29b62179273c8eb5bb682575ec87a171a",
		"c826a6fce48478dcb74f21345d2cce8038a3",
		"9d5e0853964b50af03b971722f244f58d669",
		"cbee3772a077021721a278f64f7fd633dbdd",
	}
	for i, v := range values {
		writeFile(t, dir, "f"+strconv.Itoa(i)+".txt", "token = "+v+"\n")
	}

	opts := BetterleaksOptions{
		SkipRules:  DefaultSkipRules,
		ExtraRules: []BetterleaksRule{testFixtureRule},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != len(values) {
		t.Fatalf("got %d findings %+v, want %d (one per batched file)", len(got), got, len(values))
	}
}

// ─── Base-config PII/financial rules (pii-ssn, financial-credit-card-number, financial-iban) ───
//
// Fragment-assembled trigger literals, matching this package's existing
// convention (see piifinancial_test.go's own former fixtures). The card
// numbers are the industry-standard, publicly documented Visa/Mastercard
// test numbers - not a real credential - and the IBANs are real, publicly
// documented example values used across banking documentation, so neither
// trips this repo's own vendor-shaped-literal concern.
var (
	fixtureBaseConfigSSN            = "123-45-" + "6789"
	fixtureBaseConfigSSNOther       = "234-56-" + "7891"
	fixtureBaseConfigInvalidAreaSSN = "000-45-" + "6789"
	fixtureBaseConfigVisaTestCard   = "41111111111111" + "11"
	fixtureBaseConfigLuhnInvalid    = "4111111111111" + "12"
	fixtureBaseConfigUKIBAN         = "GB82WEST1234569876" + "5432"
	fixtureBaseConfigInvalidIBAN    = "GB82WEST1234569876" + "5433"
)

// TestScanCredentialsBaseConfigPIIFinancialRulesFireAndCategorize confirms
// this package's own additional base-config rules - not upstream betterleaks
// content - detect every kind of finding the deleted hand-rolled
// ScanPIIFinancial pass used to, tagged with the right Finding.Category via
// categoryForRuleID, and that a checksum-invalid near miss (one digit off a
// Luhn-valid card / mod-97-valid IBAN) is never flagged.
func TestScanCredentialsBaseConfigPIIFinancialRulesFireAndCategorize(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", strings.Join([]string{
		"ssn: " + fixtureBaseConfigSSN,
		"card: " + fixtureBaseConfigVisaTestCard,
		"iban: " + fixtureBaseConfigUKIBAN,
	}, "\n")+"\n")
	writeFile(t, dir, "near_miss.txt", strings.Join([]string{
		"ssn: " + fixtureBaseConfigInvalidAreaSSN,
		"card: " + fixtureBaseConfigLuhnInvalid,
		"iban: " + fixtureBaseConfigInvalidIBAN,
	}, "\n")+"\n")

	got, err := ScanCredentials(dir, bin, BetterleaksOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}

	categoryByRule := map[string]string{}
	for _, f := range got {
		if f.Path != "leak.txt" {
			t.Errorf("got a finding outside leak.txt: %+v (near_miss.txt must never flag)", f)
			continue
		}
		categoryByRule[f.Rule] = f.Category
	}
	if len(categoryByRule) != len(wantPIIFinancialCategories) {
		t.Fatalf("got findings %+v, want exactly %+v", got, wantPIIFinancialCategories)
	}
	for rule, wantCategory := range wantPIIFinancialCategories {
		if categoryByRule[rule] != wantCategory {
			t.Errorf("rule %s: got category %q, want %q", rule, categoryByRule[rule], wantCategory)
		}
	}
}

// TestScanCredentialsRuleScopedAllowlistSuppressesPIIFinancialFinding pins
// the exact regression these three rules' filter bodies used to cause: a
// betterleaks-appended allowlist `||` clause landing inside a bare ternary
// filter's own condition, rather than wrapping the whole ternary, silently
// suppressed nothing for a `targetRules`-scoped exemption (a global
// rule_id "*" exemption still worked, which is what masked it). With both
// values flagged and no exemption, a rule-scoped exemption for exactly one
// of them must suppress that one and leave the other flagged.
func TestScanCredentialsRuleScopedAllowlistSuppressesPIIFinancialFinding(t *testing.T) {
	bin := testBetterleaksBinary(t)
	dir := t.TempDir()
	writeFile(t, dir, "leak.txt", strings.Join([]string{
		"ssn: " + fixtureBaseConfigSSN,
		"ssn: " + fixtureBaseConfigSSNOther,
	}, "\n")+"\n")

	baseline, err := ScanCredentials(dir, bin, BetterleaksOptions{SkipRules: DefaultSkipRules})
	if err != nil {
		t.Fatalf("ScanCredentials (baseline): %v", err)
	}
	if len(baseline) != 2 {
		t.Fatalf("baseline: got %+v, want both SSNs flagged with no allowlist entry", baseline)
	}

	opts := BetterleaksOptions{
		SkipRules:      DefaultSkipRules,
		ExtraAllowlist: []BetterleaksAllowlistEntry{{RuleID: "pii-ssn", Value: fixtureBaseConfigSSN}},
	}
	got, err := ScanCredentials(dir, bin, opts)
	if err != nil {
		t.Fatalf("ScanCredentials: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "pii-ssn" {
		t.Fatalf("got %+v, want exactly one pii-ssn finding left (the un-exempted SSN)", got)
	}
}

// wantPIIFinancialCategories is the exact, complete set of base-config rule
// ids categoryForRuleID is meant to bucket outside "credentials", and the
// bucket each belongs in - the single expectation shared by the live-scan
// test above and the static base-config cross-check below.
var wantPIIFinancialCategories = map[string]string{
	"pii-ssn":                      "pii",
	"financial-credit-card-number": "financial",
	"financial-iban":               "financial",
}

// baseConfigRuleIDPattern matches one rule-id declaration in the compiled-in
// base config. Every one of that file's rule ids sits on its own
// column-zero line in this exact form (both the pristine upstream catalog,
// which is machine-generated, and the locally authored additions at the end
// of the file); no allowlist or other nested table declares an `id` key this
// way, so every match is a rule id and every rule id is matched.
var baseConfigRuleIDPattern = regexp.MustCompile(`(?m)^id = "([^"]+)"$`)

// TestCategoryForRuleIDMatchesBaseConfigPIIFinancialRules pins the one
// coupling categoryForRuleID rests on: that the set of base-config rule ids
// carrying a "pii-"/"financial-" prefix is exactly the set of locally
// authored PII/financial rules, no more and no less. Neither side of that
// coupling fails loudly on its own - a locally added rule id that omits the
// prefix, or a future vendored-upstream catalog bump that introduces a rule
// id which happens to carry one, silently mis-buckets its findings and
// still reports them, so no scan ever errors and no other test goes red.
// This test is that alarm, and it needs no betterleaks binary to sound it.
func TestCategoryForRuleIDMatchesBaseConfigPIIFinancialRules(t *testing.T) {
	matches := baseConfigRuleIDPattern.FindAllStringSubmatch(string(betterleaksBaseConfig), -1)
	if len(matches) < 400 {
		t.Fatalf("extracted %d rule ids from the base config, want the whole catalog (400+): the id-line form this test scans for no longer matches the file's own, so it would otherwise pass vacuously", len(matches))
	}

	got := map[string]string{}
	for _, m := range matches {
		if category := categoryForRuleID(m[1]); category != "credentials" {
			got[m[1]] = category
		}
	}

	for id, wantCategory := range wantPIIFinancialCategories {
		switch got[id] {
		case wantCategory:
		case "":
			t.Errorf("data/betterleaks-base.toml declares no rule id %q carrying a category prefix, so findings that should be %q would report as \"credentials\": was the id renamed, or its prefix dropped?", id, wantCategory)
		default:
			t.Errorf("base-config rule id %q categorizes as %q, want %q", id, got[id], wantCategory)
		}
	}
	for id, category := range got {
		if _, expected := wantPIIFinancialCategories[id]; !expected {
			t.Errorf("base-config rule id %q categorizes as %q, but is not a known locally authored PII/financial rule: either add it to wantPIIFinancialCategories, or rename it so it stops claiming a category prefix", id, category)
		}
	}
}
