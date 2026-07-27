package webfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultTimeout bounds a Fetcher created with NewFetcher's zero-value
// timeout argument. Every request a Fetcher issues carries a timeout — an
// unattended pipeline must never block forever on an unresponsive origin.
const DefaultTimeout = 30 * time.Second

// DefaultUserAgent identifies requests from a Fetcher that doesn't set its
// own UserAgent.
const DefaultUserAgent = "webfetch/1.0"

// Fetcher issues bounded-timeout HTTP GETs on behalf of Sitemap, ArticleMeta,
// and Feed. The zero value is not usable; construct with NewFetcher.
type Fetcher struct {
	Client    *http.Client
	UserAgent string
}

// NewFetcher builds a Fetcher whose requests time out after timeout. A
// non-positive timeout falls back to DefaultTimeout rather than disabling
// the bound.
func NewFetcher(timeout time.Duration) *Fetcher {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Fetcher{
		Client:    &http.Client{Timeout: timeout},
		UserAgent: DefaultUserAgent,
	}
}

// fetch issues a GET against url and returns the response body. ctx governs
// cancellation in addition to the Fetcher's own client timeout; a non-2xx
// status is returned as an error rather than a body to parse.
func (f *Fetcher) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("webfetch: build request for %s: %w", url, err)
	}
	ua := f.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	req.Header.Set("User-Agent", ua)

	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webfetch: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("webfetch: fetch %s: unexpected status %s", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("webfetch: read body of %s: %w", url, err)
	}
	return body, nil
}
