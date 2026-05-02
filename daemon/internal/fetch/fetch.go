// Package fetch implements concurrent URL fetching with HTML extraction.
package fetch

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// _fetchClient is a shared http.Client with a connection pool.
//
// The original code created a new http.Client on every fetchOne call.
// That defeats Go's built-in connection pooling: each call performs a fresh
// TCP (+ TLS) handshake for every URL even when the same host is requested
// multiple times. A package-level client with a configured Transport reuses
// idle connections (up to MaxIdleConnsPerHost per host).
//
// Per-request timeouts are set via context.WithTimeout in fetchOne; we do
// NOT set http.Client.Timeout here because it would race with the request
// context and produce confusing "context deadline exceeded" errors.
var _fetchClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    false,
	},
	// Timeout intentionally omitted — callers use context.WithTimeout.
}

// validateURL rejects URLs that would cause the daemon to act as an SSRF
// proxy by forwarding requests to internal infrastructure.
//
// Checks performed:
//   - scheme must be http or https
//   - hostname must resolve via DNS
//   - all resolved IP addresses must be publicly routable:
//     loopback (127.0.0.0/8, ::1), link-local (169.254.0.0/16, fe80::/10),
//     RFC-1918 private ranges (10/8, 172.16/12, 192.168/16),
//     carrier-grade NAT shared space (100.64.0.0/10), and
//     IPv6 unique-local (fc00::/7) are all rejected.
//
// Limitation: DNS rebinding attacks can bypass this check because the IP
// may differ between the validation lookup and the actual request. This is
// acceptable for this threat model; the daemon already listens on loopback
// only, limiting the attack surface.
func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || isPrivateIP(ip) {
			return fmt.Errorf("URL %q resolves to non-routable address %s", rawURL, ip)
		}
	}
	return nil
}

// isPrivateIP reports whether ip falls within any of the non-routable
// address ranges that are off-limits for outbound fetch requests.
func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",  // RFC 6598 shared address space (carrier-grade NAT)
		"169.254.0.0/16", // IPv4 link-local (redundant with IsLinkLocalUnicast but explicit)
		"fc00::/7",       // IPv6 unique local
	}
	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// Page is the result of fetching a single URL.
type Page struct {
	URL   string `json:"url"`
	Text  string `json:"text"`
	Title string `json:"title"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Tier  int    `json:"tier"`
}

// Concurrent fetches all URLs in parallel, bounded by maxConcurrent workers.
//
// The URL slice is capped at maxURLs before allocating the result slice so
// that a caller-supplied very large list cannot cause runaway heap growth.
// The HTTP request body reader in the daemon handler already limits request
// size to 1 MiB, but an explicit cap here keeps the invariant local.
const maxURLs = 4096

func Concurrent(ctx context.Context, urls []string, maxConcurrent int, timeout time.Duration) []Page {
	if len(urls) > maxURLs {
		urls = urls[:maxURLs]
	}
	sem := make(chan struct{}, maxConcurrent)
	pages := make([]Page, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(idx int, rawURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pages[idx] = fetchOne(ctx, rawURL, timeout)
		}(i, u)
	}
	wg.Wait()
	return pages
}

// isFetchTransient reports whether an HTTP status is worth retrying on fetch.
func isFetchTransient(code int) bool {
	return code == http.StatusTooManyRequests ||
		code == http.StatusInternalServerError ||
		code == http.StatusBadGateway ||
		code == http.StatusServiceUnavailable ||
		code == http.StatusGatewayTimeout
}

// fetchBackoff returns the delay before the next fetch retry.
// A Retry-After header is honoured when present (capped at 10 s); otherwise
// exponential back-off with uniform jitter in [0, 500 ms) is used.
func fetchBackoff(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				d := time.Duration(secs) * time.Second
				if d > 10*time.Second {
					d = 10 * time.Second
				}
				return d
			}
		}
	}
	const base = 500 * time.Millisecond
	backoff := base * (1 << uint(attempt))
	jitter := time.Duration(rand.Int63n(int64(base))) //nolint:gosec // non-crypto use
	return backoff + jitter
}

// fetchOne fetches and extracts the text content of a single URL.
// Transient HTTP errors (429, 500, 502, 503, 504) are retried up to 2 times
// with exponential back-off before returning a failed Page.
func fetchOne(ctx context.Context, rawURL string, timeout time.Duration) Page {
	// Validate before issuing any network request to prevent SSRF.
	if err := validateURL(rawURL); err != nil {
		return Page{URL: rawURL, OK: false, Error: err.Error(), Tier: 1}
	}

	const maxAttempts = 3
	var lastPage Page
	var nextDelay time.Duration
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Page{URL: rawURL, OK: false, Error: ctx.Err().Error(), Tier: 2}
			case <-time.After(nextDelay):
			}
		}

		var retryDelay time.Duration
		lastPage, retryDelay = fetchAttempt(ctx, rawURL, timeout, attempt)
		if retryDelay == 0 {
			// Zero delay means no retry requested — return immediately.
			return lastPage
		}
		// Non-zero: transient error; sleep for retryDelay before the next attempt.
		nextDelay = retryDelay
		if attempt == maxAttempts-1 {
			return lastPage
		}
	}
	return lastPage
}

// fetchAttempt performs a single HTTP GET and returns (Page, retryDelay).
// retryDelay == 0 means "do not retry"; retryDelay > 0 means "retry after this delay".
//
// rawURL has already been validated by validateURL (in fetchOne) before this
// function is called: scheme is http/https, host resolves, and all resolved
// IPs are publicly routable. The CodeQL go/request-forgery alert on the
// http.NewRequestWithContext line is a false positive — the SSRF guard runs
// in the caller.
func fetchAttempt(ctx context.Context, rawURL string, timeout time.Duration, attempt int) (Page, time.Duration) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(c, "GET", rawURL, nil)
	if err != nil {
		return Page{URL: rawURL, OK: false, Error: err.Error(), Tier: 1}, 0
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; citation-researchd/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en")

	// Use the shared client with connection pooling instead of allocating a
	// new http.Client per request (which disables TCP keep-alive reuse).
	resp, err := _fetchClient.Do(req)
	if err != nil {
		// Network errors (connection refused, timeout) are retried.
		return Page{URL: rawURL, OK: false, Error: err.Error(), Tier: 2}, fetchBackoff(nil, attempt)
	}
	defer resp.Body.Close()

	if isFetchTransient(resp.StatusCode) {
		// Drain body so the connection can be reused, then signal retry.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Page{URL: rawURL, OK: false, Error: fmt.Sprintf("HTTP %d", resp.StatusCode), Tier: 3},
			fetchBackoff(resp, attempt)
	}
	if resp.StatusCode >= 400 {
		return Page{URL: rawURL, OK: false, Error: fmt.Sprintf("HTTP %d", resp.StatusCode), Tier: 3}, 0
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml") {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		return Page{URL: rawURL, OK: true, Text: string(body), Tier: 4}, 0
	}

	title, text := extractHTML(resp.Body)
	return Page{URL: rawURL, OK: true, Title: title, Text: text, Tier: 5}, 0
}

// extractHTML walks the HTML parse tree and returns (title, visible text).
func extractHTML(r io.Reader) (title, text string) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript":
				return
			case "title":
				if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					title = strings.TrimSpace(n.FirstChild.Data)
				}
				return
			}
		}
		if n.Type == html.TextNode {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				sb.WriteString(t)
				sb.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return title, sb.String()
}
