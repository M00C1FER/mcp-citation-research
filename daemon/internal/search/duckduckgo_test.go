package search

import "testing"

func TestNormalizeDDGURL_StripsRedirect(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://example.com/page", "https://example.com/page"},
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage", "https://example.com/page"},
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.com%2Fb%3Fq%3D1&rut=abc", "https://a.com/b?q=1"},
	}
	for _, c := range cases {
		got := normalizeDDGURL(c.in)
		if got != c.want {
			t.Errorf("normalizeDDGURL(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestNewDuckDuckGo_HasName(t *testing.T) {
	d := NewDuckDuckGo()
	if d.Name() != "duckduckgo" {
		t.Errorf("Name() = %q want duckduckgo", d.Name())
	}
}

func TestNewDefault_RegistersAtLeastDDG(t *testing.T) {
	// Empty SearXNG URL → DDG-only fallback registered
	m := NewDefault("")
	if len(m.Engines) != 1 {
		t.Fatalf("want 1 engine (DDG only), got %d", len(m.Engines))
	}
	if m.Engines[0].Name() != "duckduckgo" {
		t.Errorf("DDG-only fallback not registered; got %s", m.Engines[0].Name())
	}

	// SearXNG URL → both registered
	m2 := NewDefault("http://localhost:8080")
	if len(m2.Engines) != 2 {
		t.Fatalf("want 2 engines (SearXNG + DDG), got %d", len(m2.Engines))
	}
}
