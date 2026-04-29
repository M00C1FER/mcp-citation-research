// Package search implements multi-engine RRF (Reciprocal Rank Fusion) search.
// Engines: SearXNG, DuckDuckGo (HTML scrape), Brave (HTML scrape).
// All engine I/O is parallel via goroutines; RRF is in-process.
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

// Result is one ranked search hit.
type Result struct {
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Snippet string  `json:"snippet"`
	Engine  string  `json:"engine"`
	Score   float64 `json:"score"`
}

// Engine is anything that can run a query and return ranked results.
type Engine interface {
	Name() string
	Search(ctx context.Context, query string, max int) ([]Result, error)
}

// Multi runs queries across engines in parallel and RRF-fuses the results.
type Multi struct {
	Engines []Engine
	Client  *http.Client
}

// NewDefault returns a Multi configured with the SearXNG-only engine
// (the only deterministic-API engine). Callers can append DDG/Brave HTML
// scrapers as needed.
func NewDefault(searxngURL string) *Multi {
	return &Multi{
		Engines: []Engine{&SearXNG{Endpoint: searxngURL, Client: &http.Client{Timeout: 15 * time.Second}}},
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// Run executes all queries in parallel across all engines and RRF-fuses
// the deduplicated set. `maxPerQuery` is the per-engine result cap.
// `k` is the standard RRF constant (60).
func (m *Multi) Run(ctx context.Context, queries []string, maxPerQuery int, k int) []Result {
	if k <= 0 {
		k = 60
	}
	type ranked struct {
		r    Result
		rank int
	}
	allRanked := make(chan ranked, len(queries)*len(m.Engines)*maxPerQuery)
	var wg sync.WaitGroup
	for _, q := range queries {
		for _, e := range m.Engines {
			wg.Add(1)
			go func(query string, eng Engine) {
				defer wg.Done()
				results, err := eng.Search(ctx, query, maxPerQuery)
				if err != nil {
					return
				}
				for i, r := range results {
					allRanked <- ranked{r: r, rank: i + 1}
				}
			}(q, e)
		}
	}
	wg.Wait()
	close(allRanked)

	// RRF fusion: score(url) = sum(1 / (k + rank_in_engine_i))
	fused := make(map[string]*Result)
	for rk := range allRanked {
		score := 1.0 / float64(k+rk.rank)
		if existing, ok := fused[rk.r.URL]; ok {
			existing.Score += score
		} else {
			r := rk.r
			r.Score = score
			fused[r.URL] = &r
		}
	}

	// Sort by descending fused score.
	out := make([]Result, 0, len(fused))
	for _, r := range fused {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// SearXNG is a JSON-API search engine adapter.
type SearXNG struct {
	Endpoint string // e.g. "http://127.0.0.1:8080"
	Client   *http.Client
}

func (s *SearXNG) Name() string { return "searxng" }

func (s *SearXNG) Search(ctx context.Context, query string, max int) ([]Result, error) {
	if s.Endpoint == "" {
		return nil, fmt.Errorf("searxng endpoint not configured")
	}
	u := fmt.Sprintf("%s/search?q=%s&format=json", s.Endpoint, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("searxng status %d", resp.StatusCode)
	}
	var body struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]Result, 0, max)
	for i, r := range body.Results {
		if i >= max {
			break
		}
		out = append(out, Result{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: r.Content,
			Engine:  s.Name(),
		})
	}
	return out, nil
}
