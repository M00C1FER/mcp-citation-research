package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/M00C1FER/mcp-citation-research/daemon/internal/search"
	"github.com/M00C1FER/mcp-citation-research/daemon/internal/session"
)

// newTestServer returns a server wired to a fresh session Manager and a
// no-op Multi, with auth disabled (token=""). Tests that need auth should
// wrap the mux in authMiddleware themselves.
func newTestServer() (*server, *http.ServeMux) {
	srv := &server{
		sessions: session.NewManager(),
		search:   &search.Multi{Engines: nil},
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
	return srv, mux
}

// ─────────────────────────────────────────────────────────────────────────────
// /session/open
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSessionOpen_CreatesSession(t *testing.T) {
	_, mux := newTestServer()
	body := `{"topic":"quantum computing","depth":"exhaustive"}`
	r := httptest.NewRequest(http.MethodPost, "/session/open", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp["session_id"] == "" {
		t.Error("response missing session_id")
	}
}

func TestHandleSessionOpen_BadJSON(t *testing.T) {
	_, mux := newTestServer()
	r := httptest.NewRequest(http.MethodPost, "/session/open", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON, got %d", w.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// /session/update — mandate enforcement and concurrent writes
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSessionUpdate_NotFound(t *testing.T) {
	_, mux := newTestServer()
	body := `{"session_id":"does-not-exist","iteration":1,"considered":[],"fetched":[]}`
	r := httptest.NewRequest(http.MethodPost, "/session/update", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session, got %d", w.Code)
	}
}

// TestHandleSessionUpdate_MandateNotMet opens a session and calls update with
// counts below the exhaustive mandate floor, then asserts that mandate_met is
// false. This is the regression test for the four-axis enforcement gate.
func TestHandleSessionUpdate_MandateNotMet(t *testing.T) {
	_, mux := newTestServer()

	// Open a session.
	openBody := `{"topic":"test","depth":"exhaustive"}`
	r := httptest.NewRequest(http.MethodPost, "/session/open", strings.NewReader(openBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("session/open failed: %d", w.Code)
	}
	var openResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&openResp)
	sid := openResp["session_id"].(string)

	// Update with deliberately low counts (1 URL, 1 fetch, iter=1).
	updateBody := fmt.Sprintf(
		`{"session_id":%q,"iteration":1,"considered":["https://example.com/1"],"fetched":["https://example.com/1"]}`,
		sid,
	)
	r2 := httptest.NewRequest(http.MethodPost, "/session/update", strings.NewReader(updateBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("session/update failed: %d", w2.Code)
	}
	var updResp map[string]any
	_ = json.NewDecoder(w2.Body).Decode(&updResp)
	mandateMet, _ := updResp["mandate_met"].(bool)
	if mandateMet {
		t.Error("mandate_met should be false with only 1 source/iteration; gate incorrectly passed")
	}
}

// TestHandleSessionUpdate_Concurrent fires many goroutines that all POST to
// /session/update for the same session. Run with `go test -race` to detect
// data races in the handler and session.Update. The test verifies the server
// never returns a 5xx error under concurrent load.
func TestHandleSessionUpdate_Concurrent(t *testing.T) {
	_, mux := newTestServer()

	// Open a session.
	openBody := `{"topic":"concurrent","depth":"exhaustive"}`
	r := httptest.NewRequest(http.MethodPost, "/session/open", strings.NewReader(openBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var openResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&openResp)
	sid := openResp["session_id"].(string)

	const goroutines = 40
	errs := make(chan string, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(idx int) {
			defer wg.Done()
			url := fmt.Sprintf("https://concurrent%d.example.com/page", idx)
			body := fmt.Sprintf(
				`{"session_id":%q,"iteration":%d,"considered":[%q],"fetched":[%q]}`,
				sid, idx, url, url,
			)
			req := httptest.NewRequest(http.MethodPost, "/session/update", strings.NewReader(body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code >= 500 {
				errs <- fmt.Sprintf("goroutine %d got HTTP %d", idx, rec.Code)
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// /session/status
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSessionStatus_NotFound(t *testing.T) {
	_, mux := newTestServer()
	r := httptest.NewRequest(http.MethodGet, "/session/status?session_id=missing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing session, got %d", w.Code)
	}
}

func TestHandleSessionStatus_ReturnsFields(t *testing.T) {
	_, mux := newTestServer()

	openBody := `{"topic":"status-test","depth":"exhaustive"}`
	r := httptest.NewRequest(http.MethodPost, "/session/open", strings.NewReader(openBody))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var openResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&openResp)
	sid := openResp["session_id"].(string)

	r2 := httptest.NewRequest(http.MethodGet, "/session/status?session_id="+sid, nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status expected 200, got %d", w2.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w2.Body).Decode(&resp)
	for _, key := range []string{"session_id", "topic", "depth", "mandate_met"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("status response missing key %q", key)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// /session/close
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSessionClose_NotFound(t *testing.T) {
	_, mux := newTestServer()
	body := `{"session_id":"ghost"}`
	r := httptest.NewRequest(http.MethodPost, "/session/close", strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session_id, got %d", w.Code)
	}
}

func TestHandleSessionClose_RemovesSession(t *testing.T) {
	_, mux := newTestServer()

	// Open, then close.
	r := httptest.NewRequest(http.MethodPost, "/session/open",
		strings.NewReader(`{"topic":"close-me","depth":"exhaustive"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var openResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&openResp)
	sid := openResp["session_id"].(string)

	closeBody := fmt.Sprintf(`{"session_id":%q}`, sid)
	r2 := httptest.NewRequest(http.MethodPost, "/session/close", strings.NewReader(closeBody))
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("close expected 200, got %d", w2.Code)
	}

	// A subsequent status call must return 404.
	r3 := httptest.NewRequest(http.MethodGet, "/session/status?session_id="+sid, nil)
	w3 := httptest.NewRecorder()
	mux.ServeHTTP(w3, r3)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("status after close expected 404, got %d", w3.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// /search — edge cases
// ─────────────────────────────────────────────────────────────────────────────

func TestHandleSearch_BadJSON(t *testing.T) {
	_, mux := newTestServer()
	r := httptest.NewRequest(http.MethodPost, "/search",
		bytes.NewReader([]byte(`{bad json`)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed search request, got %d", w.Code)
	}
}

func TestHandleSearch_EmptyQueries(t *testing.T) {
	_, mux := newTestServer()
	r := httptest.NewRequest(http.MethodPost, "/search",
		strings.NewReader(`{"queries":[],"max":10,"k":60}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty query list, got %d", w.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if _, ok := resp["results"]; !ok {
		t.Error("response missing 'results' key")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// /healthz
// ─────────────────────────────────────────────────────────────────────────────

func TestHealthz(t *testing.T) {
	_, mux := newTestServer()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from /healthz, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("healthz response is not valid JSON: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("healthz status = %q, want %q", resp["status"], "ok")
	}
}
