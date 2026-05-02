// Package session tracks per-session research metrics and enforces a
// four-axis mandate (iterations / sources_considered / sources_fetched /
// unique_domains) before a synthesis is allowed to ship.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"sync"
	"time"
)

// Mandate is the four-axis hard floor a session must clear before
// research_synthesize will accept it.
type Mandate struct {
	Iterations        int `json:"iterations"`
	SourcesConsidered int `json:"sources_considered"`
	SourcesFetched    int `json:"sources_fetched"`
	UniqueDomains     int `json:"unique_domains"`
}

// DefaultMandate is the four-axis floor for "exhaustive" depth.
var DefaultMandate = Mandate{
	Iterations:        10,
	SourcesConsidered: 400,
	SourcesFetched:    100,
	UniqueDomains:     15,
}

// State is the live state of one research session.
type State struct {
	ID                string    `json:"session_id"`
	Topic             string    `json:"topic"`
	Depth             string    `json:"depth"`
	StartedAt         time.Time `json:"started_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	Iteration         int       `json:"iteration"`
	SourcesConsidered int       `json:"sources_considered"`
	SourcesFetched    int       `json:"sources_fetched"`
	UniqueDomains     int       `json:"unique_domains"`
	domainSeen        map[string]struct{}
	urlConsidered     map[string]struct{}
	urlFetched        map[string]struct{}
	mu                sync.Mutex
}

// DefaultSessionTTL is the lifetime of an inactive session before it is
// considered expired and evicted by Manager.Get. 24 hours is generous for a
// research session while preventing unbounded memory growth in long-running
// daemons.
const DefaultSessionTTL = 24 * time.Hour

// Manager is the in-process session registry.
type Manager struct {
	sessions   map[string]*State
	mu         sync.RWMutex
	sessionTTL time.Duration
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*State), sessionTTL: DefaultSessionTTL}
}

// NewManagerWithTTL creates a Manager whose sessions expire after ttl.
// Expiry is checked lazily on every Get call; no background goroutine is
// needed for the expected session cardinality (one per active research loop).
func NewManagerWithTTL(ttl time.Duration) *Manager {
	return &Manager{sessions: make(map[string]*State), sessionTTL: ttl}
}

// Open returns a new session with a random 16-hex-char ID.
func (m *Manager) Open(topic, depth string) *State {
	id := newID()
	s := &State{
		ID:            id,
		Topic:         topic,
		Depth:         depth,
		StartedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(m.sessionTTL),
		domainSeen:    make(map[string]struct{}),
		urlConsidered: make(map[string]struct{}),
		urlFetched:    make(map[string]struct{}),
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	return s
}

func (m *Manager) Get(id string) (*State, bool) {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(s.ExpiresAt) {
		// Lazy eviction: remove the expired session under write lock.
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		return nil, false
	}
	return s, true
}

func (m *Manager) Close(id string) (*State, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	return s, ok
}

// Update folds in a search/fetch round's URLs and bumps the iteration.
func (s *State) Update(iteration int, considered, fetched []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if iteration > s.Iteration {
		s.Iteration = iteration
	}
	for _, u := range considered {
		if _, ok := s.urlConsidered[u]; ok {
			continue
		}
		s.urlConsidered[u] = struct{}{}
		s.SourcesConsidered++
		if d := domainOf(u); d != "" {
			if _, seen := s.domainSeen[d]; !seen {
				s.domainSeen[d] = struct{}{}
				s.UniqueDomains++
			}
		}
	}
	for _, u := range fetched {
		if _, ok := s.urlFetched[u]; ok {
			continue
		}
		s.urlFetched[u] = struct{}{}
		s.SourcesFetched++
	}
}

// MandateMet returns true when all four axes have cleared the floor.
func (s *State) MandateMet(m Mandate) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Iteration >= m.Iterations &&
		s.SourcesConsidered >= m.SourcesConsidered &&
		s.SourcesFetched >= m.SourcesFetched &&
		s.UniqueDomains >= m.UniqueDomains
}

// Snapshot is the unlocked counters at a point in time, safe to return
// to callers that must not hold s.mu.
type Snapshot struct {
	Iteration         int
	SourcesConsidered int
	SourcesFetched    int
	UniqueDomains     int
	MandateMet        bool
}

// GetSnapshot returns a consistent read of all counters under s.mu.
func (s *State) GetSnapshot(m Mandate) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Iteration:         s.Iteration,
		SourcesConsidered: s.SourcesConsidered,
		SourcesFetched:    s.SourcesFetched,
		UniqueDomains:     s.UniqueDomains,
		MandateMet: s.Iteration >= m.Iterations &&
			s.SourcesConsidered >= m.SourcesConsidered &&
			s.SourcesFetched >= m.SourcesFetched &&
			s.UniqueDomains >= m.UniqueDomains,
	}
}

func domainOf(rawurl string) string {
	u, err := url.Parse(rawurl)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
