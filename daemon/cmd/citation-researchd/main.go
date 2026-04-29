// citation-researchd is the I/O daemon for mcp-citation-research.
//
// It exposes HTTP endpoints for the Python MCP frontend:
//
//	POST /search   {"queries":[...], "max":50, "k":60}
//	POST /fetch    {"urls":[...], "max_concurrent":16, "timeout_s":30}
//	POST /session/open       {"topic":"...", "depth":"exhaustive"}
//	POST /session/update     {"session_id":"...", "iteration":N, "considered":[...], "fetched":[...]}
//	GET  /session/status     ?session_id=...
//	POST /session/close      {"session_id":"..."}
//	GET  /healthz
//
// The frontend handles synthesis (LLM-bound, Python anthropic SDK).
// This daemon handles I/O (search/fetch/session) — Go's natural turf.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/M00C1FER/mcp-citation-research/daemon/internal/fetch"
	"github.com/M00C1FER/mcp-citation-research/daemon/internal/search"
	"github.com/M00C1FER/mcp-citation-research/daemon/internal/session"
)

type server struct {
	sessions *session.Manager
	search   *search.Multi
}

type searchReq struct {
	Queries []string `json:"queries"`
	Max     int      `json:"max"`
	K       int      `json:"k"`
}

type fetchReq struct {
	URLs          []string `json:"urls"`
	MaxConcurrent int      `json:"max_concurrent"`
	TimeoutS      int      `json:"timeout_s"`
}

type sessionOpenReq struct {
	Topic string `json:"topic"`
	Depth string `json:"depth"`
}

type sessionUpdateReq struct {
	SessionID  string   `json:"session_id"`
	Iteration  int      `json:"iteration"`
	Considered []string `json:"considered"`
	Fetched    []string `json:"fetched"`
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Max == 0 {
		req.Max = 50
	}
	results := s.search.Run(r.Context(), req.Queries, req.Max, req.K)
	writeJSON(w, map[string]any{"results": results, "total": len(results)})
}

func (s *server) handleFetch(w http.ResponseWriter, r *http.Request) {
	var req fetchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.MaxConcurrent == 0 {
		req.MaxConcurrent = 8
	}
	timeout := 30 * time.Second
	if req.TimeoutS > 0 {
		timeout = time.Duration(req.TimeoutS) * time.Second
	}
	pages := fetch.Concurrent(r.Context(), req.URLs, req.MaxConcurrent, timeout)
	writeJSON(w, map[string]any{"pages": pages, "total": len(pages)})
}

func (s *server) handleSessionOpen(w http.ResponseWriter, r *http.Request) {
	var req sessionOpenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st := s.sessions.Open(req.Topic, req.Depth)
	writeJSON(w, st)
}

func (s *server) handleSessionUpdate(w http.ResponseWriter, r *http.Request) {
	var req sessionUpdateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st, ok := s.sessions.Get(req.SessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	st.Update(req.Iteration, req.Considered, req.Fetched)
	writeJSON(w, map[string]any{
		"ok":                 true,
		"iteration":          st.Iteration,
		"sources_considered": st.SourcesConsidered,
		"sources_fetched":    st.SourcesFetched,
		"unique_domains":     st.UniqueDomains,
		"mandate_met":        st.MandateMet(session.DefaultMandate),
	})
}

func (s *server) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session_id")
	st, ok := s.sessions.Get(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"session_id":         st.ID,
		"topic":              st.Topic,
		"depth":              st.Depth,
		"iteration":          st.Iteration,
		"sources_considered": st.SourcesConsidered,
		"sources_fetched":    st.SourcesFetched,
		"unique_domains":     st.UniqueDomains,
		"mandate_met":        st.MandateMet(session.DefaultMandate),
	})
}

func (s *server) handleSessionClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st, ok := s.sessions.Close(req.SessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, st)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "listen address")
	searxngURL := flag.String("searxng", envOr("SEARXNG_URL", "http://127.0.0.1:8080"), "SearXNG endpoint")
	flag.Parse()

	srv := &server{
		sessions: session.NewManager(),
		search:   search.NewDefault(*searxngURL),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/search", srv.handleSearch)
	mux.HandleFunc("/fetch", srv.handleFetch)
	mux.HandleFunc("/session/open", srv.handleSessionOpen)
	mux.HandleFunc("/session/update", srv.handleSessionUpdate)
	mux.HandleFunc("/session/status", srv.handleSessionStatus)
	mux.HandleFunc("/session/close", srv.handleSessionClose)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("citation-researchd listening on %s (searxng=%s)", *addr, *searxngURL)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	// Block until SIGINT/SIGTERM (handled by Go's default signal forwarding).
	<-context.Background().Done()
	_ = httpSrv.Close()
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
