package webfetch

import (
	"fmt"
	"net/url"
	"strings"
)

// canonicalizeURL normalizes raw into the form Sitemap and Feed use as a
// dedup key: lower-cased scheme/host, no fragment, no trailing slash except
// on the bare root path. It rejects anything without both a scheme and a
// host, since a relative or malformed reference has no stable identity to
// dedup on.
func canonicalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("webfetch: empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("webfetch: parse url %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("webfetch: url %q has no scheme/host", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}
	return u.String(), nil
}
