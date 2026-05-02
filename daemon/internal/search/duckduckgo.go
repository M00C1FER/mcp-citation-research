package search

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// DuckDuckGo is a zero-dependency HTML-scrape engine that works without any
// local infrastructure. Useful as a fallback when SearXNG is not available.
//
// It hits https://html.duckduckgo.com/html/?q=<query> and parses the result
// list out of the rendered HTML. No API key, no rate-limit headers, but DDG
// reserves the right to throttle aggressive callers — keep concurrency low.
type DuckDuckGo struct {
	Endpoint string // default "https://html.duckduckgo.com/html/"
	Client   *http.Client
}

func NewDuckDuckGo() *DuckDuckGo {
	return &DuckDuckGo{
		Endpoint: "https://html.duckduckgo.com/html/",
		Client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (d *DuckDuckGo) Name() string { return "duckduckgo" }

func (d *DuckDuckGo) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	q := url.Values{"q": {query}}
	req, err := http.NewRequestWithContext(ctx, "POST", d.Endpoint, strings.NewReader(q.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "citation-researchd/0.1 (+https://github.com/M00C1FER/mcp-citation-research)")
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("duckduckgo status %d", resp.StatusCode)
	}
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}
	results := parseDDGResults(doc, maxResults)
	return results, nil
}

// parseDDGResults walks the DDG HTML tree and collects result tuples.
// DDG's HTML page has stable class names: result__a (link/title), result__snippet (snippet).
func parseDDGResults(doc *html.Node, max int) []Result {
	// Cap the initial allocation to a reasonable ceiling so a caller-supplied
	// large max value cannot cause a runaway heap allocation.
	const maxCap = 200
	if max > maxCap {
		max = maxCap
	}
	out := make([]Result, 0, max)
	var titleHref, titleText, snippet string

	flush := func() {
		if titleHref != "" && titleText != "" {
			cleanURL := normalizeDDGURL(titleHref)
			if cleanURL != "" && len(out) < max {
				out = append(out, Result{
					URL:     cleanURL,
					Title:   strings.TrimSpace(titleText),
					Snippet: strings.TrimSpace(snippet),
					Engine:  "duckduckgo",
				})
			}
		}
		titleHref, titleText, snippet = "", "", ""
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil || len(out) >= max {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			class := attr(n, "class")
			href := attr(n, "href")
			if strings.Contains(class, "result__a") && href != "" {
				flush()
				titleHref = href
				titleText = textOf(n)
			}
		}
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			if strings.Contains(class, "result__snippet") {
				snippet = textOf(n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	flush()
	return out
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// normalizeDDGURL strips DDG's redirect wrapper (//duckduckgo.com/l/?uddg=...&...)
// and returns the underlying target URL.
func normalizeDDGURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Host == "duckduckgo.com" && strings.HasPrefix(u.Path, "/l/") {
		if uddg := u.Query().Get("uddg"); uddg != "" {
			if dec, err := url.QueryUnescape(uddg); err == nil {
				return dec
			}
		}
		return ""
	}
	return raw
}
