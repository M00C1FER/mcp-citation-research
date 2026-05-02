package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// SearXNG — malformed / error responses
// ─────────────────────────────────────────────────────────────────────────────

// TestSearXNG_MalformedJSON verifies that a non-JSON response body returns an
// error rather than an empty slice, so callers can distinguish a broken
// SearXNG instance from a genuine "no results" response.
func TestSearXNG_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("this is not json {{{"))
	}))
	defer srv.Close()

	engine := &SearXNG{BaseURL: srv.URL}
	_, err := engine.Search(context.Background(), "test query", 10)
	if err == nil {
		t.Error("SearXNG.Search: expected error for malformed JSON response, got nil")
	}
}

// TestSearXNG_HTTPError verifies that a non-200 status (e.g. 503 during an
// outage) propagates as an error rather than silently returning no results.
func TestSearXNG_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	engine := &SearXNG{BaseURL: srv.URL}
	_, err := engine.Search(context.Background(), "test query", 10)
	if err == nil {
		t.Error("SearXNG.Search: expected error for HTTP 503 response, got nil")
	}
}

// TestSearXNG_ValidResponse exercises the happy path: a well-formed JSON
// response is parsed into the expected Result slice.
func TestSearXNG_ValidResponse(t *testing.T) {
	body := `{"results":[
		{"url":"https://example.com/1","title":"First","content":"snippet one"},
		{"url":"https://example.com/2","title":"Second","content":"snippet two"}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	engine := &SearXNG{BaseURL: srv.URL}
	results, err := engine.Search(context.Background(), "test", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("results[0].URL = %q, want %q", results[0].URL, "https://example.com/1")
	}
	if results[0].Engine != "searxng" {
		t.Errorf("results[0].Engine = %q, want %q", results[0].Engine, "searxng")
	}
}

// TestSearXNG_MaxResultsCap verifies that SearXNG respects the max parameter
// and returns at most max results even when the server returns more.
func TestSearXNG_MaxResultsCap(t *testing.T) {
	body := `{"results":[
		{"url":"https://a.com/1","title":"A1","content":"c1"},
		{"url":"https://a.com/2","title":"A2","content":"c2"},
		{"url":"https://a.com/3","title":"A3","content":"c3"}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	engine := &SearXNG{BaseURL: srv.URL}
	results, err := engine.Search(context.Background(), "test", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("expected at most 2 results with max=2, got %d", len(results))
	}
}

// TestSearXNG_EmptyResults verifies that an empty result array is valid and
// does not trigger an error.
func TestSearXNG_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	engine := &SearXNG{BaseURL: srv.URL}
	results, err := engine.Search(context.Background(), "obscure query", 10)
	if err != nil {
		t.Fatalf("unexpected error for empty result list: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Multi.Run — outage scenarios
// ─────────────────────────────────────────────────────────────────────────────

// TestMultiRun_SearXNGOutage verifies that when SearXNG is unreachable the
// Multi runner returns within a reasonable time and does not hang or panic.
// The DuckDuckGo engine is also replaced with a stub to avoid real network I/O.
func TestMultiRun_AllEnginesError(t *testing.T) {
	// Both engines fail. Run should return an empty slice, not hang.
	alwaysErr := &stubEngine{name: "failing", err: true}
	m := &Multi{Engines: []Engine{alwaysErr, alwaysErr}}

	results := m.Run(context.Background(), []string{"query"}, 5, 60)
	if results == nil {
		t.Error("Run returned nil; want an empty (non-nil) slice")
	}
	if len(results) != 0 {
		t.Errorf("Run returned %d results from failing engines, want 0", len(results))
	}
}

// TestMultiRun_OneEngineOK confirms that when one engine fails the other
// engine's results are still returned.
func TestMultiRun_OneEngineOK(t *testing.T) {
	good := &stubEngine{name: "good", results: []Result{
		{URL: "https://good.example.com/1", Title: "Good", Engine: "good"},
	}}
	bad := &stubEngine{name: "bad", err: true}

	m := &Multi{Engines: []Engine{good, bad}}
	results := m.Run(context.Background(), []string{"query"}, 5, 60)
	if len(results) == 0 {
		t.Error("Run returned no results; expected results from the healthy engine")
	}
}

// TestMultiRun_RRFFusion verifies that the same URL appearing in two engine
// results has its RRF score accumulated (not duplicated as two entries).
func TestMultiRun_RRFFusion(t *testing.T) {
	shared := Result{URL: "https://shared.example.com/", Title: "Shared"}
	e1 := &stubEngine{name: "e1", results: []Result{shared}}
	e2 := &stubEngine{name: "e2", results: []Result{shared}}

	m := &Multi{Engines: []Engine{e1, e2}}
	results := m.Run(context.Background(), []string{"q"}, 5, 60)

	// Exactly one entry for the shared URL.
	count := 0
	for _, r := range results {
		if r.URL == shared.URL {
			count++
		}
	}
	if count != 1 {
		t.Errorf("fused result list has %d entries for shared URL, want 1", count)
	}
	// Its score should be higher than 1/(k+1) because two engines contributed.
	for _, r := range results {
		if r.URL == shared.URL && r.Score <= 1.0/float64(60+1) {
			t.Errorf("fused score %f is not higher than single-engine score %f; RRF fusion may be broken",
				r.Score, 1.0/float64(60+1))
		}
	}
}

// TestMultiRun_CancelledContext verifies that cancelling the context before
// Run is called does not deadlock or panic.
func TestMultiRun_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	slow := &stubEngine{name: "slow", results: []Result{
		{URL: "https://slow.example.com/", Title: "Slow"},
	}}
	m := &Multi{Engines: []Engine{slow}}
	// Should return quickly (stub ignores context, so this mainly checks
	// that Run itself doesn't deadlock when launched with a dead context).
	done := make(chan struct{})
	go func() {
		m.Run(ctx, []string{"q"}, 5, 60)
		close(done)
	}()
	select {
	case <-done:
		// ok — returned without deadlock
	case <-timeAfter(2):
		t.Fatal("Run deadlocked with a cancelled context")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// stubEngine — test double
// ─────────────────────────────────────────────────────────────────────────────

type stubEngine struct {
	name    string
	results []Result
	err     bool
}

func (s *stubEngine) Name() string { return s.name }
func (s *stubEngine) Search(_ context.Context, _ string, _ int) ([]Result, error) {
	if s.err {
		return nil, &searchError{msg: "stub engine error"}
	}
	return s.results, nil
}

type searchError struct{ msg string }

func (e *searchError) Error() string { return e.msg }

// timeAfter returns a channel that fires after n seconds.
func timeAfter(n int) <-chan time.Time {
	return time.After(time.Duration(n) * time.Second)
}
