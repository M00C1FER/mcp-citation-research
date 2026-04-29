// Package fetch performs concurrent URL fetching with bounded parallelism,
// readability extraction, and structured output suitable for RAG ingestion.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// Page is a fetched URL plus extracted text content.
type Page struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	WordCount int    `json:"word_count"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Status    int    `json:"status"`
	Tier      int    `json:"tier"`
}

// Concurrent fetches URLs with at most `maxConcurrent` in flight.
// Each URL is fetched once, with `timeout` per request.
func Concurrent(ctx context.Context, urls []string, maxConcurrent int, timeout time.Duration) []Page {
	if maxConcurrent < 1 {
		maxConcurrent = 8
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	sem := make(chan struct{}, maxConcurrent)
	out := make([]Page, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, url string) {
			defer wg.Done()
			defer func() { <-sem }()
			out[idx] = fetchOne(ctx, url, timeout)
		}(i, u)
	}
	wg.Wait()
	return out
}

func fetchOne(ctx context.Context, url string, timeout time.Duration) Page {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(c, "GET", url, nil)
	if err != nil {
		return Page{URL: url, OK: false, Error: err.Error(), Tier: 1}
	}
	req.Header.Set("User-Agent", "citation-research/0.1 (+https://github.com/M00C1FER/mcp-citation-research)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return Page{URL: url, OK: false, Error: err.Error(), Tier: 1}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Page{URL: url, OK: false, Status: resp.StatusCode, Error: fmt.Sprintf("status %d", resp.StatusCode), Tier: 1}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MiB cap
	if err != nil {
		return Page{URL: url, OK: false, Status: resp.StatusCode, Error: err.Error(), Tier: 1}
	}
	title, content := extractText(string(body))
	wc := wordCount(content)
	return Page{
		URL:       url,
		Title:     title,
		Content:   content,
		WordCount: wc,
		OK:        wc >= 30,
		Status:    resp.StatusCode,
		Tier:      1,
	}
}

// extractText is a minimal readability-style extractor: grabs <title> and
// concatenates visible text from <p>, <li>, <h*>, skipping <script>/<style>.
func extractText(htmlSrc string) (title, content string) {
	doc, err := html.Parse(strings.NewReader(htmlSrc))
	if err != nil {
		return "", ""
	}
	var titleBuf, bodyBuf strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inBlock bool) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "nav", "footer", "aside":
				return
			case "title":
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						titleBuf.WriteString(c.Data)
					}
				}
				return
			case "p", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "article", "section":
				inBlock = true
			}
		}
		if n.Type == html.TextNode && inBlock {
			t := strings.TrimSpace(n.Data)
			if t != "" {
				bodyBuf.WriteString(t)
				bodyBuf.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inBlock)
		}
	}
	walk(doc, false)
	return strings.TrimSpace(titleBuf.String()), strings.TrimSpace(bodyBuf.String())
}

func wordCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Fields(s))
}
