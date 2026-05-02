// Package search implements multi-engine RRF-fused web search.
//
// Usage:
//
//m := search.NewDefault("http://127.0.0.1:8080")
//results := m.Run(ctx, []string{"query"}, 50, 60)
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

// Result is a single web-search result.
type Result struct {
URL     string  `json:"url"`
Title   string  `json:"title"`
Snippet string  `json:"snippet"`
Engine  string  `json:"engine,omitempty"`
Score   float64 `json:"score"`
}

// Engine is the interface that wraps a single search backend.
type Engine interface {
// Name returns a short identifier for the engine (e.g. "searxng", "duckduckgo").
Name() string
Search(ctx context.Context, query string, max int) ([]Result, error)
}

// Multi fans out queries across several engines and fuses results via RRF.
type Multi struct {
Engines []Engine
}

// NewDefault builds a Multi with the engines that are available in a standard
// NEXUS deployment.
//
//   - When searxngURL is non-empty both SearXNG and DuckDuckGo are registered
//     (SearXNG first so its results appear higher in early RRF rounds).
//   - When searxngURL is empty only DuckDuckGo is registered as a zero-
//     infrastructure fallback.
func NewDefault(searxngURL string) *Multi {
engines := []Engine{NewDuckDuckGo()}
if searxngURL != "" {
engines = []Engine{&SearXNG{BaseURL: searxngURL}, NewDuckDuckGo()}
}
return &Multi{Engines: engines}
}

// Run issues all (query, engine) pairs concurrently and returns the
// RRF-fused result list sorted by descending score.
//
// maxPerQuery is the per-engine result cap for a single query. Non-positive
// values are treated as 50. k is the RRF smoothing constant (default 60).
//
// The original implementation used a buffered channel sized
// len(queries)*len(engines)*maxPerQuery. When maxPerQuery was 0 that
// produced an unbuffered channel, causing every goroutine to block on send
// because the consumer (range allRanked) ran only after wg.Wait() — a
// classic goroutine deadlock. Negative values panicked with
// make(chan T, negative). The fix collects into a mutex-protected slice:
// goroutines never block, and we iterate the slice after wg.Wait().
func (m *Multi) Run(ctx context.Context, queries []string, maxPerQuery int, k int) []Result {
if k <= 0 {
k = 60
}
if maxPerQuery <= 0 {
maxPerQuery = 50
}

type ranked struct {
r    Result
rank int
}

var mu sync.Mutex
var collected []ranked

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
// Build local slice before locking to minimise contention.
local := make([]ranked, 0, len(results))
for i, r := range results {
local = append(local, ranked{r: r, rank: i + 1})
}
mu.Lock()
collected = append(collected, local...)
mu.Unlock()
}(q, e)
}
}
wg.Wait()

// RRF fusion: score(url) = sum(1 / (k + rank_in_engine_i))
fused := make(map[string]*Result)
for _, rk := range collected {
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

// SearXNG wraps a running SearXNG instance.
type SearXNG struct {
BaseURL string
}

func (s *SearXNG) Name() string { return "searxng" }

// Search queries the SearXNG JSON API.
func (s *SearXNG) Search(ctx context.Context, query string, max int) ([]Result, error) {
client := &http.Client{Timeout: 10 * time.Second}
apiURL := fmt.Sprintf("%s/search?q=%s&format=json&pageno=1",
s.BaseURL, url.QueryEscape(query))
req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
if err != nil {
return nil, err
}
resp, err := client.Do(req)
if err != nil {
return nil, err
}
defer resp.Body.Close()

var payload struct {
Results []struct {
URL     string `json:"url"`
Title   string `json:"title"`
Content string `json:"content"`
} `json:"results"`
}
if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
return nil, err
}
results := make([]Result, 0, len(payload.Results))
for i, r := range payload.Results {
if i >= max {
break
}
results = append(results, Result{
URL:     r.URL,
Title:   r.Title,
Snippet: r.Content,
Engine:  "searxng",
})
}
return results, nil
}
