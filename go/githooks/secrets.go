package githooks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// secretPattern is one high-confidence plaintext-secret signature.
type secretPattern struct {
	re    *regexp.Regexp
	label string
}

// labelAWSAccessKeyID is the AWS-access-key-id rule id, named once because
// matchesSecretPattern dispatches its exemption on it: a rename here stays in
// sync with the pattern table by construction, where two string literals
// would silently drift and disable the exemption. It is also the stable
// Finding.Rule value callers key on, so its text is a public contract.
const labelAWSAccessKeyID = "aws_access_key_id"

// labelSlackToken is the Slack-token rule id, named for the same reason as
// labelAWSAccessKeyID: matchesSecretPattern's exemption lookup dispatches on
// it, and a rename here stays in sync with the pattern table by
// construction.
const labelSlackToken = "slack_token"

// secretPatterns are the closed, high-confidence signatures ScanSecrets looks
// for: private-key blocks and cloud/VCS/chat-host token shapes distinctive
// enough that a match is never mistaken for ordinary source text.
//
// private_key_block requires real body content after the header, not just
// the header line by itself: [\s\\]{0,4} tolerates up to four characters of
// line-break representation between the header and the body - a real
// newline byte in an ordinary source file, or the two-character `\n` escape
// sequence as it literally appears inside a JSON string value in a .jsonl
// corpus - and [A-Za-z0-9+/=]{40,} then requires at least 40 consecutive
// base64-alphabet characters. A real key's own first body line clears that
// easily (PEM wraps at 64 characters per line); a bare keyword mention with
// no body at all, a short filler placeholder, or a truncated example ending
// in non-base64 characters all fall short and no longer match.
var secretPatterns = []secretPattern{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\\]{0,4}[A-Za-z0-9+/=]{40,}`), "private_key_block"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), labelAWSAccessKeyID},
	{regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}`), labelSlackToken},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`), "github_token"},
}

// awsExampleAccessKeyIDs holds the exact, enumerated AWS documentation
// placeholder access-key ids (and any future sibling AWS itself reserves the
// same way) that the AWS-access-key-id pattern must never flag - AWS's
// key-generation process can never produce these exact strings, so an exact
// match is provably not a real, leaked credential. Exempt by exact string
// only, never by substring or "contains EXAMPLE" heuristic.
//
// The value below is fragment-assembled, not because it needs hiding from a
// reader, but because a pre-fix scanner landing this very commit has no
// allowlist yet and would otherwise refuse the merge over its own source
// line.
var awsExampleAccessKeyIDs = map[string]bool{
	"AKIAIOSFODNN7" + "EXAMPLE": true,
}

// slackExampleTokens holds the exact, enumerated Slack-token-shaped strings
// that the Slack-token pattern must never flag - each one is a documented
// example format from a third-party secret-detection tool's own
// rule-definition file, illustrating what that tool's Slack-token rule
// matches, not a bot token any Slack workspace ever issued. Exempt by exact
// string only, never by substring or "contains EXAMPLE" heuristic.
//
// The value below is fragment-assembled for the same reason as
// awsExampleAccessKeyIDs's: a pre-fix scanner landing this very commit has no
// allowlist yet and would otherwise refuse the merge over its own source
// line.
var slackExampleTokens = map[string]bool{
	"xoxb-ab59" + "EXAMPLETOKEN": true,
}

// exactExemptions maps a pattern's label to its exact-match exemption set.
// A label with no entry here has no exact-match exemption at all.
var exactExemptions = map[string]map[string]bool{
	labelAWSAccessKeyID: awsExampleAccessKeyIDs,
	labelSlackToken:     slackExampleTokens,
}

// compiledSecretAllowlistEntry is a caller-supplied ScanSecrets allowlist
// entry (see BetterleaksAllowlistEntry) with its Regex, if any, pre-compiled
// once per ScanSecrets call rather than once per file.
type compiledSecretAllowlistEntry struct {
	ruleID string
	value  string
	re     *regexp.Regexp
}

// appliesToRule reports whether e is scoped to label: an empty or "*"
// RuleID applies to every pattern, matching BetterleaksAllowlistEntry's own
// targetRules-less-entry semantics.
func (e compiledSecretAllowlistEntry) appliesToRule(label string) bool {
	return e.ruleID == "" || e.ruleID == "*" || e.ruleID == label
}

// matches reports whether e exempts occurrence: an exact string match
// against e.value, or a partial (unanchored) match against e.re.
func (e compiledSecretAllowlistEntry) matches(occurrence string) bool {
	if e.re != nil {
		return e.re.MatchString(occurrence)
	}
	return e.value == occurrence
}

// compileSecretAllowlist validates and compiles a caller's extra allowlist
// entries once, up front, so a malformed entry fails before any file is
// scanned rather than mid-walk. Mirrors buildBetterleaksConfig's own
// exactly-one-of-Value-or-Regex validation.
func compileSecretAllowlist(entries []BetterleaksAllowlistEntry) ([]compiledSecretAllowlistEntry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	compiled := make([]compiledSecretAllowlistEntry, 0, len(entries))
	for _, e := range entries {
		if (e.Value == "") == (e.Regex == "") {
			return nil, fmt.Errorf("githooks: ScanSecrets extra allowlist entry (RuleID %q) must set exactly one of Value or Regex", e.RuleID)
		}
		var re *regexp.Regexp
		if e.Regex != "" {
			compiledRe, err := regexp.Compile(e.Regex)
			if err != nil {
				return nil, fmt.Errorf("githooks: ScanSecrets extra allowlist entry (RuleID %q): invalid Regex %q: %w", e.RuleID, e.Regex, err)
			}
			re = compiledRe
		}
		compiled = append(compiled, compiledSecretAllowlistEntry{ruleID: e.RuleID, value: e.Value, re: re})
	}
	return compiled, nil
}

// matchesSecretPattern reports whether text contains a hit for p that isn't
// covered by p's compiled-in exact-match exemption set, if it has one (see
// exactExemptions), AND isn't covered by extra, the caller's own allowlist
// entries (see compileSecretAllowlist) - two independent, additive layers.
// Every occurrence is checked; the file is flagged only if at least one
// occurrence is exempted by neither layer.
func matchesSecretPattern(p secretPattern, text string, extra []compiledSecretAllowlistEntry) bool {
	exempt, hasExact := exactExemptions[p.label]
	if !hasExact && len(extra) == 0 {
		return p.re.MatchString(text)
	}
	for _, match := range p.re.FindAllString(text, -1) {
		if hasExact && exempt[match] {
			continue
		}
		exemptedByExtra := false
		for _, e := range extra {
			if e.appliesToRule(p.label) && e.matches(match) {
				exemptedByExtra = true
				break
			}
		}
		if !exemptedByExtra {
			return true
		}
	}
	return false
}

// ScanSecrets walks root and reports every file whose text matches one of the
// closed set of high-confidence secret signatures - private-key blocks, AWS
// access-key ids, Slack and GitHub tokens. Two patterns carry a compiled-in
// exact-match exemption against a vendor's own permanently reserved
// documentation placeholder value (see awsExampleAccessKeyIDs and
// slackExampleTokens), which provably cannot be a real credential; a
// near-miss or any other key shape is still reported. extraAllowlist is a
// second, independent exemption layer the caller supplies at call time (see
// compileSecretAllowlist): an entry whose RuleID is empty, "*", or one of
// the four pattern labels, and whose Value exactly equals or Regex matches
// (partially - not required to match the whole occurrence) a matched
// occurrence, exempts that occurrence the same way a compiled-in exact
// exemption does. Neither layer ever replaces the other; a file is flagged
// only if at least one occurrence clears both. A third, independent
// exemption is path-based: a file secretExemptRules resolves to SkipClass
// via fsx.ClassifyPath is never scanned for secrets at all, for a
// corpus/fixture path whose content is verified safe but happens to match a
// secret-shaped regex - the same exemption ScanPrivacy's secret check
// honors via PrivacyOptions.SecretExemptRules, so a consumer configures it
// once and gets identical behavior from either scan path. skipRules is the
// injected fsx.ClassifyPath ruleset naming directories/paths never scanned
// at all (VCS internals, build artifacts, vendored trees); a binary-asset
// extension is always skipped regardless of skipRules. A file that fails to
// decode as UTF-8 is treated as unreadable-as-text, not scanned, and never
// reported - there is nothing to leak in text form.
//
// A secretExemptRules pattern that is not a valid glob, or an extraAllowlist
// entry that sets neither or both of Value/Regex, or whose Regex fails to
// compile, returns an error before any file is read, since an unparseable
// exempt pattern would otherwise exempt the whole tree from the scan. A
// malformed skipRules pattern is not an error: it keeps fsx.ClassifyPath's
// cautious skip-the-path default.
func ScanSecrets(root string, skipRules, secretExemptRules []fsx.Rule, extraAllowlist []BetterleaksAllowlistEntry) ([]Finding, error) {
	if err := validateExemptRules(secretExemptRuleset, secretExemptRules); err != nil {
		return nil, err
	}
	compiledExtra, err := compileSecretAllowlist(extraAllowlist)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	walkErr := walkScannable(root, skipRules, func(rel, abs string) error {
		if binarySuffixes[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}
		if fsx.ClassifyPath(rel, secretExemptRules).Class == SkipClass {
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
		for _, p := range secretPatterns {
			if matchesSecretPattern(p, text, compiledExtra) {
				findings = append(findings, Finding{Path: rel, Rule: p.label, Detail: "possible " + strings.ReplaceAll(p.label, "_", " ")})
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return findings, nil
}
