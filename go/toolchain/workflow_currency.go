package toolchain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// releasesFileEnv names the environment variable that switches the currency
// rule's release-record source from the platform API to a local JSON file
// (WFRULES clause (e)). It is the only path to a testable warning verdict and a
// testable error verdict: SC15 leaves the fleet with one release, so every real
// caller names the highest tag and returns the current (silent) verdict, and
// only a file can present the older releases the warn and error verdicts need.
const releasesFileEnv = "LANGUAGE_TOOLS_RELEASES_FILE"

// currencyWindow is fleet rule two's currency window (WFRULES clause (c)): an
// older release of the same major version passes with a warning while it is
// younger than this, and fails with an error once it reaches it. The boundary
// is inclusive — at exactly ninety days the error verdict applies — so no
// release age falls outside a verdict.
const (
	currencyWindowDays = 90
	currencyWindow     = currencyWindowDays * 24 * time.Hour
)

// noticeReason is the closed reason a silent verdict names — the reader-facing
// explanation for why fleet rule one or fleet rule two declined to gate a body
// or a reference without failing it. WFRULES clause (d) closes the set at
// exactly seven members: each emits one notice, a notice fails no step, and the
// rule never fails on an unresolved lookup, so one throttled hour cannot
// disable the rule fleet-wide while every check still reports green. The set is
// proven closed by an exhaustiveness test, not asserted in a comment.
type noticeReason string

const (
	// The four failed-lookup reasons. The rule reads the release record from
	// the platform API (F78 measures the template repository public, so no
	// credential is needed), and the lookup can still fail four ways.
	reasonNoNetwork    noticeReason = "no_network"
	reasonNoCredential noticeReason = "no_credential"
	reasonRateLimited  noticeReason = "rate_limited"
	reasonServerError  noticeReason = "server_error"
	// no_tag covers a ref resolving to nothing; path_ref covers a path-named
	// ref, which carries no version to compare (DD8); not_a_check_subject
	// covers a body outside fleet rule one's subject definition.
	reasonNoTag            noticeReason = "no_tag"
	reasonPathRef          noticeReason = "path_ref"
	reasonNotACheckSubject noticeReason = "not_a_check_subject"
)

// allNoticeReasons is the canonical closed set WFRULES clause (d) pins, in the
// clause's own order: the four lookup-failure reasons, then no_tag, path_ref
// and not_a_check_subject. An exhaustiveness test asserts every member is
// reachable and no eighth is, so a silently unhandled case cannot return green.
var allNoticeReasons = []noticeReason{
	reasonNoNetwork, reasonNoCredential, reasonRateLimited, reasonServerError,
	reasonNoTag, reasonPathRef, reasonNotACheckSubject,
}

// valid reports whether r is one of the seven canonical reasons. It is the one
// gate a reason passes through, so an invented eighth is rejected rather than
// carried.
func (r noticeReason) valid() bool {
	for _, m := range allNoticeReasons {
		if r == m {
			return true
		}
	}
	return false
}

// verdictKind is fleet rule two's closed outcome for one template reference.
// Only verdictWarn and verdictError yield a Diagnostic; verdictCurrent and
// verdictSilent leave the run green, the first with no annotation and the
// second with a notice the notice layer emits from the verdict's reason.
type verdictKind int

const (
	// verdictCurrent: the caller names the highest release. Success, no
	// annotation, no reason (WFRULES clause (c)).
	verdictCurrent verdictKind = iota
	// verdictWarn: an older release of the same major version, published inside
	// the currency window. Success with one warning annotation.
	verdictWarn
	// verdictError: a release behind by one or more major versions, or
	// published at or past the window. Failure with one error annotation.
	verdictError
	// verdictSilent: the lookup did not resolve, or the ref is not one the rule
	// gates. Success with one notice annotation naming reason.
	verdictSilent
)

// currencyVerdict is one template reference's evaluated outcome under fleet
// rule two. reason is set only when kind is verdictSilent; message carries the
// human-facing detail a warn or error annotation shows.
type currencyVerdict struct {
	kind    verdictKind
	reason  noticeReason
	message string
}

func currentVerdict() currencyVerdict { return currencyVerdict{kind: verdictCurrent} }
func silentVerdict(r noticeReason) currencyVerdict {
	return currencyVerdict{kind: verdictSilent, reason: r}
}
func warnVerdict(msg string) currencyVerdict { return currencyVerdict{kind: verdictWarn, message: msg} }
func errorVerdict(msg string) currencyVerdict {
	return currencyVerdict{kind: verdictError, message: msg}
}

// diagnostic renders a verdict as the Diagnostic Run classifies, or ok=false
// when the verdict carries none. A warn verdict is a SeverityWarning — a
// warning-carrying run classifies as success with warnings, which the binary's
// result layer promotes to caveats (exit 10, per F83), so the currency warning
// passes the step rather than gating it. An error verdict is a SeverityError,
// which classifies as gate_negative (exit 20). A current or silent verdict
// yields nothing: the run stays green, and a silent verdict's reason rides on
// the verdict value for the notice layer rather than on the RunResult.
func (v currencyVerdict) diagnostic(rel string) (Diagnostic, bool) {
	switch v.kind {
	case verdictWarn:
		return Diagnostic{
			Severity: SeverityWarning,
			Code:     "template_currency",
			Message:  fmt.Sprintf("%s currency: %s", actionlintTool, v.message),
			File:     rel,
		}, true
	case verdictError:
		return Diagnostic{
			Severity: SeverityError,
			Code:     "template_currency",
			Message:  fmt.Sprintf("%s currency: %s", actionlintTool, v.message),
			File:     rel,
		}, true
	default:
		return Diagnostic{}, false
	}
}

// evaluateCurrency applies fleet rule two to one template reference against the
// resolved release catalog, at run-start time now (WFRULES clause (c)). A path
// ref is silent at reason path_ref before any lookup, per DD8. A nil catalog
// means the lookup failed with the classified lookupReason, which the verdict
// then names. Otherwise the ref is resolved to a tag: a ref resolving to no tag
// is silent at reason no_tag; the highest release is current; a release behind
// by a major version, or a same-major release at or past the window, is an
// error; and a same-major release inside the window is a warning. The two
// lookup outcomes that look alike differ here: a tag carrying no release record
// takes that tag's commit date (taggedRelease.date), while a ref resolving to
// no tag returns the silent verdict.
func evaluateCurrency(ref templateRef, catalog *releaseCatalog, lookupReason noticeReason, now time.Time) currencyVerdict {
	if ref.isPath {
		return silentVerdict(reasonPathRef)
	}
	if catalog == nil {
		return silentVerdict(lookupReason)
	}
	highest, ok := catalog.highestBareVersion()
	if !ok {
		return silentVerdict(reasonNoTag)
	}
	tag, ok := catalog.find(ref.version)
	if !ok {
		return silentVerdict(reasonNoTag)
	}
	if tag.name == highest.name {
		return currentVerdict()
	}
	if tag.version.major < highest.version.major {
		return errorVerdict(fmt.Sprintf("references %s, behind the current %s by a major version", tag.name, highest.name))
	}
	// Same major, older release: the window decides, inclusive at the boundary.
	if now.Before(tag.date().Add(currencyWindow)) {
		return warnVerdict(fmt.Sprintf("references %s, behind the current %s but inside the %d-day window", tag.name, highest.name, currencyWindowDays))
	}
	return errorVerdict(fmt.Sprintf("references %s, behind the current %s and published %d or more days before the run", tag.name, highest.name, currencyWindowDays))
}

// templateRef is one `uses:` reference fleet rule two reads: a reference naming
// a workflow in the template repository, at a version tag or a same-repository
// path. An official-action reference names another owner and is not a
// templateRef, so it stays outside the rule (K5).
type templateRef struct {
	raw     string // the full uses: value, for a deterministic ordering
	isPath  bool   // a same-repository path reference (./… or ../…), per DD8
	version string // the ref after '@' for a cross-repository call, else ""
}

// bareVersionTagRE matches a bare semver version tag — vMAJOR.MINOR.PATCH with
// no path prefix and no pre-release suffix. A module tag (go/toolchain/v0.3.0)
// and a crate tag (rust/clikit/v0.1.0) carry a path prefix, so neither matches
// and neither joins the comparison set, per DD9; a pre-release is excluded by
// K11's stable-only rule.
var bareVersionTagRE = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// parseTemplateRef reads a `uses:` value into a templateRef, reporting false
// when the value names neither the template repository nor a same-repository
// path (an official action, a third-party reusable workflow, or a docker
// reference).
func parseTemplateRef(uses string) (templateRef, bool) {
	uses = strings.TrimSpace(uses)
	if uses == "" {
		return templateRef{}, false
	}
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "../") {
		return templateRef{raw: uses, isPath: true}, true
	}
	at := strings.LastIndex(uses, "@")
	if at < 0 {
		return templateRef{}, false
	}
	path, ref := uses[:at], uses[at+1:]
	if !strings.HasPrefix(path, templateRepo+"/") {
		return templateRef{}, false
	}
	return templateRef{raw: uses, version: ref}, true
}

// isBareVersionTag reports whether a cross-repository reference names the
// template at a bare version tag — the compliant form fleet rule one requires
// of a caller outside the template repository.
func (r templateRef) isBareVersionTag() bool {
	return !r.isPath && bareVersionTagRE.MatchString(r.version)
}

// semver is the parsed core of a bare version tag, compared component by
// component. It is never compared with < on the raw string, which orders v10
// before v2.
type semver struct {
	major, minor, patch int
}

// parseSemver parses a bare vMAJOR.MINOR.PATCH tag, reporting false for
// anything else (a path-prefixed module or crate tag, a branch name, a commit
// SHA).
func parseSemver(name string) (semver, bool) {
	if !bareVersionTagRE.MatchString(name) {
		return semver{}, false
	}
	parts := strings.Split(name[1:], ".")
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return semver{major: major, minor: minor, patch: patch}, true
}

// less reports whether a precedes b in semver order.
func (a semver) less(b semver) bool {
	if a.major != b.major {
		return a.major < b.major
	}
	if a.minor != b.minor {
		return a.minor < b.minor
	}
	return a.patch < b.patch
}

// taggedRelease is one tag of the template repository as the currency rule sees
// it: its name, its parsed version (for the bare tags only), whether it carries
// a release record, and the two dates the rule may read. date resolves the one
// the rule uses.
type taggedRelease struct {
	name       string
	version    semver
	isBare     bool
	hasRelease bool
	published  time.Time
	commit     time.Time
}

// date is the date fleet rule two ages: the release's published_at when a
// release record exists, and the tag's commit date otherwise (WFRULES clause
// (c)'s two look-alike outcomes — a tag carrying no release record ages by its
// commit).
func (t taggedRelease) date() time.Time {
	if t.hasRelease {
		return t.published
	}
	return t.commit
}

// releaseCatalog is the template repository's tag set as one resolved release
// source returned it.
type releaseCatalog struct {
	tags []taggedRelease
}

// find returns the tag whose name equals name, and whether one exists.
func (c *releaseCatalog) find(name string) (taggedRelease, bool) {
	for _, t := range c.tags {
		if t.name == name {
			return t, true
		}
	}
	return taggedRelease{}, false
}

// highestBareVersion returns the highest bare version tag by semver, and false
// when the catalog holds none — the comparison set DD9 defines.
func (c *releaseCatalog) highestBareVersion() (taggedRelease, bool) {
	var best taggedRelease
	found := false
	for _, t := range c.tags {
		if !t.isBare {
			continue
		}
		if !found || best.version.less(t.version) {
			best, found = t, true
		}
	}
	return best, found
}

// releaseSource is the switchable origin of the template repository's release
// records (WFRULES clause (e)). catalog returns the resolved catalog, or a
// classified lookup reason with a nil catalog when the lookup failed one of the
// four ways, so the rule stays silent rather than failing. A non-nil error is
// an infrastructure failure the caller propagates — a set-but-unreadable
// releases file — never a lookup outcome.
type releaseSource interface {
	catalog(ctx context.Context) (*releaseCatalog, noticeReason, error)
}

// resolveReleaseSource selects the source per WFRULES clause (e): the local
// JSON file when LANGUAGE_TOOLS_RELEASES_FILE names one, the platform API
// otherwise. An unset variable falls back to the API — never to silence — so a
// real caller still reaches a live lookup rather than a no-op.
func resolveReleaseSource() releaseSource {
	if path := os.Getenv(releasesFileEnv); path != "" {
		return fileReleaseSource{path: path}
	}
	return apiReleaseSource{
		repo:   templateRepo,
		base:   githubAPIBase,
		client: &http.Client{Timeout: apiTimeout},
	}
}

// releasesFileDoc is the LANGUAGE_TOOLS_RELEASES_FILE schema: every tag of the
// template repository, each with its commit date and, when a release record
// exists, its published_at. A tag with a published_at carries a release record;
// one without ages by its commit date.
type releasesFileDoc struct {
	Tags []struct {
		Name        string `json:"name"`
		CommitDate  string `json:"commit_date"`
		PublishedAt string `json:"published_at"`
	} `json:"tags"`
}

// fileReleaseSource reads the catalog from the local JSON file the test seam
// names. A missing or malformed file is an infrastructure failure, not a silent
// verdict: the seam is set deliberately, so a broken one is loud rather than
// swallowed.
type fileReleaseSource struct {
	path string
}

func (s fileReleaseSource) catalog(context.Context) (*releaseCatalog, noticeReason, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, "", fmt.Errorf("toolchain: read %s=%s: %w", releasesFileEnv, s.path, err)
	}
	var doc releasesFileDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, "", fmt.Errorf("toolchain: parse %s=%s: %w", releasesFileEnv, s.path, err)
	}
	cat := &releaseCatalog{}
	for _, t := range doc.Tags {
		tr, err := buildTaggedRelease(t.Name, t.CommitDate, t.PublishedAt)
		if err != nil {
			return nil, "", fmt.Errorf("toolchain: %s tag %q: %w", releasesFileEnv, t.Name, err)
		}
		cat.tags = append(cat.tags, tr)
	}
	return cat, "", nil
}

// buildTaggedRelease assembles one tag from its name and its two optional dates
// (RFC 3339). A present published_at marks a release record.
func buildTaggedRelease(name, commitDate, publishedAt string) (taggedRelease, error) {
	ver, isBare := parseSemver(name)
	tr := taggedRelease{name: name, version: ver, isBare: isBare}
	if commitDate != "" {
		c, err := time.Parse(time.RFC3339, commitDate)
		if err != nil {
			return taggedRelease{}, fmt.Errorf("commit_date %q: %w", commitDate, err)
		}
		tr.commit = c.UTC()
	}
	if publishedAt != "" {
		p, err := time.Parse(time.RFC3339, publishedAt)
		if err != nil {
			return taggedRelease{}, fmt.Errorf("published_at %q: %w", publishedAt, err)
		}
		tr.published = p.UTC()
		tr.hasRelease = true
	}
	return tr, nil
}

// githubAPIBase, apiTimeout and maxAPIBytes bound the default API source. The
// base is a field on apiReleaseSource so a test can point it at a local server;
// the timeout keeps a hung endpoint from stalling the check; the byte cap keeps
// an oversized body from exhausting memory.
const (
	githubAPIBase = "https://api.github.com"
	apiTimeout    = 15 * time.Second
	maxAPIBytes   = 4 << 20
)

// apiReleaseSource reads the catalog from the GitHub REST API. It is the
// default source and the one every real caller reaches; SC15 leaves one
// release, so a real caller resolves the highest tag and returns silent. A
// failed lookup is classified into one of the four reasons rather than raised,
// so a throttled hour surfaces as a green run with a notice.
type apiReleaseSource struct {
	repo   string
	base   string
	client *http.Client
}

func (s apiReleaseSource) catalog(ctx context.Context) (*releaseCatalog, noticeReason, error) {
	var releases []struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
	}
	if reason, ok := s.fetch(ctx, s.base+"/repos/"+s.repo+"/releases?per_page=100", &releases); !ok {
		return nil, reason, nil
	}
	published := make(map[string]string, len(releases))
	for _, r := range releases {
		published[r.TagName] = r.PublishedAt
	}

	var tags []struct {
		Name   string `json:"name"`
		Commit struct {
			URL string `json:"url"`
		} `json:"commit"`
	}
	if reason, ok := s.fetch(ctx, s.base+"/repos/"+s.repo+"/tags?per_page=100", &tags); !ok {
		return nil, reason, nil
	}

	cat := &releaseCatalog{}
	for _, t := range tags {
		commitDate := ""
		if _, hasRelease := published[t.Name]; !hasRelease && t.Commit.URL != "" {
			// No release record: fleet rule two falls back to the commit date,
			// so fetch the commit this tag points at.
			var commit struct {
				Commit struct {
					Committer struct {
						Date string `json:"date"`
					} `json:"committer"`
				} `json:"commit"`
			}
			if reason, ok := s.fetch(ctx, t.Commit.URL, &commit); !ok {
				return nil, reason, nil
			}
			commitDate = commit.Commit.Committer.Date
		}
		tr, err := buildTaggedRelease(t.Name, commitDate, published[t.Name])
		if err != nil {
			return nil, "", fmt.Errorf("toolchain: %s release record for %q: %w", s.repo, t.Name, err)
		}
		cat.tags = append(cat.tags, tr)
	}
	return cat, "", nil
}

// fetch performs one GET and decodes a 200 body into out, or returns the
// classified reason and ok=false for any failure. A transport error is
// no_network; 401 is no_credential; 403 with an exhausted rate-limit header or
// 429 is rate_limited; any other 403 is no_credential; a 5xx or an unreadable
// or unparseable 200 body is server_error.
func (s apiReleaseSource) fetch(ctx context.Context, url string, out any) (noticeReason, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return reasonNoNetwork, false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client.Do(req)
	if err != nil {
		return reasonNoNetwork, false
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIBytes))
		if err != nil {
			return reasonServerError, false
		}
		if err := json.Unmarshal(body, out); err != nil {
			return reasonServerError, false
		}
		return "", true
	case http.StatusUnauthorized:
		return reasonNoCredential, false
	case http.StatusForbidden:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return reasonRateLimited, false
		}
		return reasonNoCredential, false
	case http.StatusTooManyRequests:
		return reasonRateLimited, false
	default:
		return reasonServerError, false
	}
}
