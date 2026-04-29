package session

import "testing"

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
