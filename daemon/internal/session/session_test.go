package session

import (
	"sync"
	"testing"
	"time"
)

func TestMandateMet(t *testing.T) {
	mgr := NewManager()
	s := mgr.Open("test", "exhaustive")
	if s.MandateMet(DefaultMandate) {
		t.Fatal("fresh session should not meet mandate")
	}
	// Push enough URLs to clear iter+considered+fetched+domains
	considered := make([]string, 0, 401)
	for i := 0; i < 401; i++ {
		considered = append(considered, fakeURL(i))
	}
	fetched := considered[:101]
	s.Update(11, considered, fetched)

	if got, want := s.SourcesConsidered, 401; got != want {
		t.Errorf("considered=%d want %d", got, want)
	}
	if got, want := s.SourcesFetched, 101; got != want {
		t.Errorf("fetched=%d want %d", got, want)
	}
	if got := s.UniqueDomains; got < 15 {
		t.Errorf("unique_domains=%d want >=15", got)
	}
	if !s.MandateMet(DefaultMandate) {
		t.Errorf("mandate should be met: iter=%d considered=%d fetched=%d domains=%d",
			s.Iteration, s.SourcesConsidered, s.SourcesFetched, s.UniqueDomains)
	}
}

func TestUpdateDeduplicates(t *testing.T) {
	mgr := NewManager()
	s := mgr.Open("dedup", "exhaustive")
	s.Update(1, []string{"https://a.com/1", "https://a.com/2"}, []string{"https://a.com/1"})
	s.Update(2, []string{"https://a.com/1", "https://a.com/3"}, []string{"https://a.com/1"}) // dups
	if got, want := s.SourcesConsidered, 3; got != want {
		t.Errorf("considered=%d want %d (dedup failed)", got, want)
	}
	if got, want := s.SourcesFetched, 1; got != want {
		t.Errorf("fetched=%d want %d (dedup failed)", got, want)
	}
}

// TestMandateBoundaryTable verifies that a session with exactly one axis
// below the exhaustive mandate floor never satisfies MandateMet, and that
// meeting all axes (at the floor) does satisfy it.
func TestMandateBoundaryTable(t *testing.T) {
	type tc struct {
		name      string
		iteration int
		considered int
		fetched   int
		domains   int
		wantMet   bool
	}
	cases := []tc{
		{"all_zero", 0, 0, 0, 0, false},
		{"iter_short_by_one", 9, 400, 100, 15, false},
		{"considered_short_by_one", 10, 399, 100, 15, false},
		{"fetched_short_by_one", 10, 400, 99, 15, false},
		{"domains_short_by_one", 10, 400, 100, 14, false},
		{"all_at_floor", 10, 400, 100, 15, true},
		{"all_above_floor", 20, 500, 200, 30, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mgr := NewManager()
			s := mgr.Open("boundary", "exhaustive")

			// Build URL lists with an explicit domain spread so that
			// UniqueDomains == c.domains regardless of URL count.
			considered := makeURLsWithDomains(c.considered, c.domains)
			fetched := makeURLsWithDomains(c.fetched, c.domains)

			s.Update(c.iteration, considered, fetched)

			if got := s.MandateMet(DefaultMandate); got != c.wantMet {
				t.Errorf("MandateMet = %v, want %v (iter=%d considered=%d fetched=%d domains=%d)",
					got, c.wantMet, s.Iteration, s.SourcesConsidered, s.SourcesFetched, s.UniqueDomains)
			}
		})
	}
}

// makeURLsWithDomains generates n unique URLs spread across numDomains distinct
// hostnames so the caller can control UniqueDomains independently of URL count.
// numDomains is clamped to n to avoid a modulo-by-zero panic.
func makeURLsWithDomains(n, numDomains int) []string {
	if n == 0 {
		return nil
	}
	if numDomains <= 0 {
		numDomains = 1
	}
	if numDomains > n {
		numDomains = n
	}
	urls := make([]string, n)
	for i := 0; i < n; i++ {
		d := itoa(i % numDomains)
		urls[i] = "https://domain" + d + ".example.com/" + itoa(i)
	}
	return urls
}

// TestSessionExpiry verifies that Manager.Get returns (nil, false) for a
// session whose TTL has elapsed, and that the session is lazily evicted.
func TestSessionExpiry(t *testing.T) {
	mgr := NewManagerWithTTL(50 * time.Millisecond)
	s := mgr.Open("expiry-topic", "exhaustive")
	id := s.ID

	// Session is immediately accessible.
	if _, ok := mgr.Get(id); !ok {
		t.Fatal("session should be accessible immediately after Open")
	}

	// Wait for TTL to lapse.
	time.Sleep(100 * time.Millisecond)

	if _, ok := mgr.Get(id); ok {
		t.Error("session should have expired but was still returned by Get")
	}
	// Eviction: a second Get must also miss (not panic or return stale data).
	if _, ok := mgr.Get(id); ok {
		t.Error("session should remain expired on second Get after eviction")
	}
}

// TestSessionNotExpiredBeforeTTL confirms that a session is still accessible
// when queried well before its TTL elapses.
func TestSessionNotExpiredBeforeTTL(t *testing.T) {
	mgr := NewManagerWithTTL(10 * time.Second)
	s := mgr.Open("long-lived", "exhaustive")
	id := s.ID
	if _, ok := mgr.Get(id); !ok {
		t.Error("session opened with 10s TTL must be accessible immediately")
	}
}

// TestConcurrentUpdates fires many goroutines that Update the same session
// simultaneously. Run with `go test -race` to detect data races.
func TestConcurrentUpdates(t *testing.T) {
	mgr := NewManager()
	s := mgr.Open("concurrent", "exhaustive")

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(idx int) {
			defer wg.Done()
			// Each goroutine contributes a unique URL to avoid dedup masking
			// any missing lock.
			u := "https://race" + itoa(idx) + ".example.com/page"
			s.Update(idx, []string{u}, []string{u})
		}(g)
	}
	wg.Wait()

	// Weak post-condition: at most goroutines URLs may have been counted.
	if s.SourcesConsidered > goroutines {
		t.Errorf("SourcesConsidered=%d exceeds goroutine count %d", s.SourcesConsidered, goroutines)
	}
}

func fakeURL(i int) string {
	// rotate across many domains so unique_domains > 15
	domains := []string{
		"a.com", "b.com", "c.com", "d.com", "e.com", "f.com", "g.com", "h.com",
		"i.com", "j.com", "k.com", "l.com", "m.com", "n.com", "o.com", "p.com",
		"q.com", "r.com", "s.com", "t.com",
	}
	d := domains[i%len(domains)]
	return "https://" + d + "/" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	out := ""
	for i > 0 {
		out = string(rune('0'+i%10)) + out
		i /= 10
	}
	return out
}
