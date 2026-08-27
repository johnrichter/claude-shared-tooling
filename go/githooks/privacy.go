package githooks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// PrivacyTier is which forbidden-marker/internal-identifier posture
// ScanPrivacy applies. It is the parameter that lets one scanner serve every
// repo instead of a tier fixed per fork - a caller picks the tier, the
// scanner is otherwise identical.
type PrivacyTier string

// The closed set of privacy tiers, strictest to loosest. Public is a repo
// shared outside the org; Datadog is shared inside the org; Personal is a
// single owner's own private repo.
const (
	TierPublic   PrivacyTier = "public"
	TierDatadog  PrivacyTier = "datadog"
	TierPersonal PrivacyTier = "personal"
)

// Known reports whether t is one of the three defined tiers.
func (t PrivacyTier) Known() bool {
	_, ok := privacyTierConfigs[t]
	return ok
}

// privacyTierConfig is one tier's forbidden-marker set, whether the
// "declares-but-not-tier-public" pair check applies, and which
// internal-identifier posture it uses.
type privacyTierConfig struct {
	forbiddenMarkers  []markerPattern
	requirePublicPair bool
	internalID        []markerPattern
}

type markerPattern struct {
	re    *regexp.Regexp
	label string
}

var privacyTierConfigs = map[PrivacyTier]privacyTierConfig{
	TierPublic: {
		forbiddenMarkers: []markerPattern{
			{regexp.MustCompile(`(?i)\bprivacy:\s*(internal|confidential)\b`), "forbidden frontmatter marker"},
			{regexp.MustCompile(`(?i)\bowner:\s*(datadog|personal)\b`), "forbidden frontmatter marker"},
		},
		requirePublicPair: true,
		internalID:        internalIDStrict,
	},
	TierDatadog: {
		forbiddenMarkers: []markerPattern{
			{regexp.MustCompile(`(?i)\bprivacy:\s*confidential\b`), "forbidden frontmatter marker"},
			{regexp.MustCompile(`(?i)\bowner:\s*personal\b`), "forbidden frontmatter marker"},
		},
		requirePublicPair: false,
		internalID:        internalIDRelaxed,
	},
	TierPersonal: {
		forbiddenMarkers:  nil,
		requirePublicPair: false,
		internalID:        nil,
	},
}

// fmPairChecks are the public-tier "declares a tag but not its public value"
// checks: a file whose frontmatter declares privacy/owner at all but not the
// public value (catches an unenumerated value, e.g. "privacy: restricted").
var fmPairChecks = []struct {
	any    *regexp.Regexp
	public *regexp.Regexp
	key    string
}{
	{regexp.MustCompile(`(?i)\bprivacy:\s*\w+`), regexp.MustCompile(`(?i)\bprivacy:\s*public\b`), "privacy"},
	{regexp.MustCompile(`(?i)\bowner:\s*\w+`), regexp.MustCompile(`(?i)\bowner:\s*public\b`), "owner"},
}

// hostTerminator pins the private-network match to a true end-of-host: an
// optional numeric port, then a path/query/fragment delimiter, closing
// punctuation, whitespace, or end of string. A "." (a further host label) is
// deliberately not a terminator, so a private-range token that is only the
// leading label of a longer hostname (e.g. 192.168.1.1.example.com, a DNS
// name and not a private address) does not match.
const hostTerminator = `(?:(?::\d+)?(?:[/?#\s"'<>),]|$))`

var privateNetworkURL = regexp.MustCompile(
	`https?://(?:localhost|127\.\d{1,3}\.\d{1,3}\.\d{1,3}|10\.\d{1,3}\.\d{1,3}\.\d{1,3}` +
		`|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3})` +
		hostTerminator)

// internalIDStrict is the public-tier internal-identifier posture: internal
// hostnames, private-network URLs, issue-tracker links and employee emails -
// none of which match a bare company-name mention in prose.
var internalIDStrict = []markerPattern{
	{regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*\.(?:corp|internal|intranet|lan)\b`), "internal hostname"},
	{privateNetworkURL, "private/loopback network URL"},
	{regexp.MustCompile(`(?i)\b(?:jira|atlassian|confluence)\.[\w.-]+/(?:browse|wiki)/[A-Za-z][\w-]*`), "internal issue-tracker/wiki link"},
	{regexp.MustCompile(`(?i)\b[\w.+-]+@(?:datadoghq\.com|datadoghq\.internal)\b`), "internal employee email"},
}

// internalIDRelaxed is the datadog-tier posture: internal hostnames/emails/
// wiki links are expected inside an org-shared repo, so only network-private
// addresses stay flagged.
var internalIDRelaxed = []markerPattern{
	{privateNetworkURL, "private/loopback network URL"},
}

// publicEmailAllowlist holds the exact, enumerated public role addresses at
// datadoghq.com (support, sales, press, ...) that the employee-email pattern
// must never flag - a function anyone external is meant to reach, never an
// individual. Exempt by exact address only, never by domain or wildcard.
var publicEmailAllowlist = map[string]bool{
	"support@datadoghq.com": true, "sales@datadoghq.com": true,
	"press@datadoghq.com": true, "info@datadoghq.com": true,
	"privacy@datadoghq.com": true, "security@datadoghq.com": true,
	"legal@datadoghq.com": true, "careers@datadoghq.com": true,
}

// PrivacyOptions parameterizes ScanPrivacy beyond the tier itself.
type PrivacyOptions struct {
	// SkipRules is the injected fsx.ClassifyPath ruleset for directories/
	// paths never scanned at all (VCS internals, build artifacts).
	SkipRules []fsx.Rule
	// MarkerExemptRules is a second, independent fsx.ClassifyPath ruleset:
	// paths resolving to SkipClass here still get the secret/internal-id
	// scan, but never the frontmatter-marker checks. Intended for source
	// code and fixture/corpus directories that legitimately embed literal
	// marker strings as data, not as a real sensitivity/owner declaration.
	MarkerExemptRules []fsx.Rule
	// SecretExemptRules is a third, independent fsx.ClassifyPath ruleset:
	// paths resolving to SkipClass here still get the frontmatter-marker and
	// internal-id checks, but never the secret-pattern scan. Intended for
	// machine-generated corpus/fixture files whose content is verified-safe
	// but happens to match a secret-shaped regex (e.g. a security tool's own
	// pattern definitions, or scraped documentation examples), so the whole
	// path doesn't have to be pulled out of every other check to fix one
	// false positive.
	SecretExemptRules []fsx.Rule
}

// ScanPrivacy applies tier's forbidden-marker and internal-identifier
// postures to every file under root not excluded by opts.SkipRules,
// returning (failures, warnings). A failure always fails the caller's build;
// a warning (internal-identifier mentions) is caller's choice whether to
// treat as failing, matching the source guardrail's default-warn/--strict-
// fails posture.
//
// Marker checks (forbidden markers, and the public-tier declares-but-not-
// public pair check) run only against each file's own leading `---`/`---`
// frontmatter block, and are skipped entirely for a file opts.
// MarkerExemptRules resolves to SkipClass. The secret-pattern check runs
// whole-file and is skipped entirely for a file opts.SecretExemptRules
// resolves to SkipClass (see PrivacyOptions.SecretExemptRules); its other
// exemptions are by exact matched value (see awsExampleAccessKeyIDs). The
// internal-identifier check runs whole-file with no path exemption; its only
// exemption is by exact matched value (see publicEmailAllowlist). Each of
// the three checks has its own independent exemption mechanism, so
// exempting a path from one never exempts it from the others.
//
// A pattern in MarkerExemptRules or SecretExemptRules that is not a valid
// glob returns an error naming the ruleset and the pattern before any file is
// read, since an unparseable exempt pattern would otherwise exempt the whole
// tree from that check. A malformed opts.SkipRules pattern is not an error:
// it keeps fsx.ClassifyPath's cautious skip-the-path default.
func ScanPrivacy(root string, tier PrivacyTier, opts PrivacyOptions) (failures, warnings []Finding, err error) {
	cfg, ok := privacyTierConfigs[tier]
	if !ok {
		return nil, nil, fmt.Errorf("githooks: unknown privacy tier %q", tier)
	}
	if err := validateExemptRules(markerExemptRuleset, opts.MarkerExemptRules); err != nil {
		return nil, nil, err
	}
	if err := validateExemptRules(secretExemptRuleset, opts.SecretExemptRules); err != nil {
		return nil, nil, err
	}

	walkErr := walkScannable(root, opts.SkipRules, func(rel, abs string) error {
		if binarySuffixes[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}
		data, readErr := os.ReadFile(abs)
		if readErr != nil || !isValidUTF8(data) {
			return nil // unreadable/binary — nothing to leak in text form
		}
		text := string(data)

		if fsx.ClassifyPath(rel, opts.MarkerExemptRules).Class != SkipClass {
			fm := frontmatterBlock(text)
			for _, m := range cfg.forbiddenMarkers {
				for _, match := range m.re.FindAllString(fm, -1) {
					failures = append(failures, Finding{Path: rel, Rule: "forbidden_marker", Detail: fmt.Sprintf("forbidden frontmatter marker %q", match)})
				}
			}
			if cfg.requirePublicPair {
				for _, pc := range fmPairChecks {
					if pc.any.MatchString(fm) && !pc.public.MatchString(fm) {
						failures = append(failures, Finding{Path: rel, Rule: "not_public_pair", Detail: fmt.Sprintf("frontmatter declares %s: tag but not %s:public", pc.key, pc.key)})
					}
				}
			}
		}

		if fsx.ClassifyPath(rel, opts.SecretExemptRules).Class != SkipClass {
			for _, p := range secretPatterns {
				if matchesSecretPattern(p, text) {
					failures = append(failures, Finding{Path: rel, Rule: p.label, Detail: "possible " + strings.ReplaceAll(p.label, "_", " ")})
				}
			}
		}

		for _, m := range cfg.internalID {
			for _, match := range m.re.FindAllString(text, -1) {
				if m.label == "internal employee email" && publicEmailAllowlist[strings.ToLower(match)] {
					continue
				}
				warnings = append(warnings, Finding{Path: rel, Rule: "internal_identifier", Detail: fmt.Sprintf("internal identifier — %s", m.label)})
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	return failures, warnings, nil
}

// frontmatterBlock returns the raw text of text's leading `---`/`---`
// frontmatter block (the first fenced block at file head only), or "" if
// there is none or the fence is unterminated.
func frontmatterBlock(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n")
		}
	}
	return ""
}
