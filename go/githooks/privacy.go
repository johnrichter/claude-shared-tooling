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
// shared outside the org; Confidential is shared inside the org; Private is a
// single repo not shared at all.
const (
	TierPublic       PrivacyTier = "public"
	TierConfidential PrivacyTier = "confidential"
	TierPrivate      PrivacyTier = "private"
)

// employeeEmailLabel is the internal-identifier label used by both the
// employee-email pattern and its allowlist lookup, named once so the two
// stay in sync by construction.
const employeeEmailLabel = "internal employee email"

// internalHostnameLabel is the internal-identifier label used by both the
// internal-hostname pattern and its reserved-sentinel filter, named once so
// the two stay in sync by construction.
const internalHostnameLabel = "internal hostname"

// Known reports whether t is one of the three defined tiers.
func (t PrivacyTier) Known() bool {
	_, ok := privacyTierConfigs[t]
	return ok
}

// privacyTierConfig is one tier's forbidden-marker set, whether the
// "declares-but-not-tier-public" pair check applies, which internal-
// identifier posture it uses, and whether that posture is eligible for the
// caller-configured employee-email check (see PrivacyOptions.EmployeeEmail).
type privacyTierConfig struct {
	forbiddenMarkers    []markerPattern
	requirePublicPair   bool
	internalID          []markerPattern
	checksEmployeeEmail bool
}

type markerPattern struct {
	re    *regexp.Regexp
	label string
}

var privacyTierConfigs = map[PrivacyTier]privacyTierConfig{
	TierPublic: {
		forbiddenMarkers: []markerPattern{
			{regexp.MustCompile(`(?i)\bprivacy:\s*(internal|confidential)\b`), "forbidden frontmatter marker"},
		},
		requirePublicPair:   true,
		internalID:          internalIDStrict,
		checksEmployeeEmail: true,
	},
	TierConfidential: {
		forbiddenMarkers: []markerPattern{
			{regexp.MustCompile(`(?i)\bprivacy:\s*confidential\b`), "forbidden frontmatter marker"},
		},
		requirePublicPair: false,
		internalID:        internalIDRelaxed,
	},
	TierPrivate: {
		forbiddenMarkers:  nil,
		requirePublicPair: false,
		internalID:        nil,
	},
}

// fmPairChecks are the public-tier "declares a tag but not its public value"
// checks: a file whose frontmatter declares privacy: at all but not
// privacy:public (catches an unenumerated value, e.g. "privacy: restricted").
var fmPairChecks = []struct {
	any    *regexp.Regexp
	public *regexp.Regexp
	key    string
}{
	{regexp.MustCompile(`(?i)\bprivacy:\s*\w+`), regexp.MustCompile(`(?i)\bprivacy:\s*public\b`), "privacy"},
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

// reservedSentinelSuffix matches an RFC 6761 reserved-TLD label (.invalid,
// .test, .localhost, or .example - including the RFC 2606 example.{com,net,
// org} second-level domains) anchored at the start of whatever immediately
// follows an internal-hostname match, itself followed by hostTerminator: a
// genuine end of host, never another "." introducing more host content. Go's
// RE2 engine has no lookahead, so unlike the negative-lookahead this filter
// mirrors, it is applied by the caller against the text right after a match
// rather than embedded in the hostname pattern itself - the same exclusion,
// expressed as a post-match check instead of a zero-width assertion.
var reservedSentinelSuffix = regexp.MustCompile(
	`(?i)^\.(?:invalid|test|example(?:\.(?:com|net|org))?|localhost)` + hostTerminator)

// internalIDStrict is the public-tier internal-identifier posture: internal
// hostnames, private-network URLs, and issue-tracker links - none of which
// match a bare company-name mention in prose. The employee-email check is a
// fourth, caller-configured member of this posture (see
// PrivacyOptions.EmployeeEmail); it is appended per call, not baked in here,
// since this module ships no default domain of its own.
//
// The internal-hostname pattern's match ends right at the word boundary
// after corp/internal/intranet/lan, so it matches equally whether that
// label is the true end of the host (a real internal address) or is
// immediately followed by an RFC 6761 reserved sentinel TLD (e.g.
// "host.corp.test", a documentation/fixture hostname, not a real one). The
// caller in ScanPrivacy filters out the latter via reservedSentinelSuffix,
// keyed off internalHostnameLabel.
var internalIDStrict = []markerPattern{
	{regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*\.(?:corp|internal|intranet|lan)\b`), internalHostnameLabel},
	{privateNetworkURL, "private/loopback network URL"},
	{regexp.MustCompile(`(?i)\b(?:jira|atlassian|confluence)\.[\w.-]+/(?:browse|wiki)/[A-Za-z][\w-]*`), "internal issue-tracker/wiki link"},
}

// internalIDRelaxed is the confidential-tier posture: internal hostnames/
// emails/wiki links are expected inside an org-shared repo, so only
// network-private addresses stay flagged. The employee-email check never
// applies at this tier, configured or not (see privacyTierConfig.
// checksEmployeeEmail).
var internalIDRelaxed = []markerPattern{
	{privateNetworkURL, "private/loopback network URL"},
}

// EmployeeEmailCheck configures the optional employee-email member of the
// public tier's internal-identifier posture: an address at one of Domains is
// flagged as an internal identifier unless it exactly matches Allowlist (a
// public role address anyone external is meant to reach, e.g.
// support@example.com - never a wildcard or domain-wide exemption).
//
// The zero value disables the check entirely: this module ships no default
// domain, so a caller that wants this check active must supply its own
// Domains (and, optionally, Allowlist).
type EmployeeEmailCheck struct {
	// Domains are matched as literal text, never as patterns: a regex
	// metacharacter in an entry is escaped and matches only itself, so an
	// entry that is not a real domain simply never matches. A blank or
	// whitespace-only entry is dropped rather than compiled, since an empty
	// alternation branch would match every address at every domain; if no
	// entry survives, the check stays off.
	Domains []string
	// Allowlist exempts an address by its full text, compared
	// case-insensitively, so a key's casing never silently defeats the
	// caller's own exemption. There is no wildcard or domain-wide form: an
	// entry exempts exactly one address.
	Allowlist map[string]bool
}

// employeeEmailPattern compiles c.Domains into the "internal employee email"
// markerPattern, or returns false if c configures no usable domain (the check
// is off). Each domain is escaped, so a caller-supplied string is always
// matched literally and can never inject regex syntax into the pattern.
func (c EmployeeEmailCheck) employeeEmailPattern() (markerPattern, bool) {
	escaped := make([]string, 0, len(c.Domains))
	for _, d := range c.Domains {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		escaped = append(escaped, regexp.QuoteMeta(d))
	}
	if len(escaped) == 0 {
		return markerPattern{}, false
	}
	re := regexp.MustCompile(`(?i)\b[\w.+-]+@(?:` + strings.Join(escaped, "|") + `)\b`)
	return markerPattern{re, employeeEmailLabel}, true
}

// allowlistIndex returns c.Allowlist rekeyed by lowercased address, matching
// how a matched address is looked up. Exempt entries are kept and non-exempt
// ones dropped, so two keys differing only in case resolve to "exempt"
// deterministically rather than by map iteration order.
func (c EmployeeEmailCheck) allowlistIndex() map[string]bool {
	if len(c.Allowlist) == 0 {
		return nil
	}
	index := make(map[string]bool, len(c.Allowlist))
	for addr, exempt := range c.Allowlist {
		if exempt {
			index[strings.ToLower(addr)] = true
		}
	}
	return index
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
	// marker strings as data, not as a real sensitivity declaration.
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
	// EmployeeEmail configures the public tier's optional employee-email
	// check (see EmployeeEmailCheck). Its zero value leaves the check off:
	// this module ships no default domain or allowlist of its own, so a
	// caller who wants it active supplies its own Domains and Allowlist.
	EmployeeEmail EmployeeEmailCheck
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
// exemption is by exact matched value (see PrivacyOptions.EmployeeEmail.
// Allowlist). Each of the three checks has its own independent exemption
// mechanism, so exempting a path from one never exempts it from the others.
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

	internalID := cfg.internalID
	var emailAllowlist map[string]bool
	if cfg.checksEmployeeEmail {
		if p, on := opts.EmployeeEmail.employeeEmailPattern(); on {
			internalID = append(append([]markerPattern{}, cfg.internalID...), p)
			emailAllowlist = opts.EmployeeEmail.allowlistIndex()
		}
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

		for _, m := range internalID {
			for _, span := range m.re.FindAllStringIndex(text, -1) {
				match := text[span[0]:span[1]]
				if m.label == employeeEmailLabel && emailAllowlist[strings.ToLower(match)] {
					continue
				}
				if m.label == internalHostnameLabel && reservedSentinelSuffix.MatchString(text[span[1]:]) {
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
