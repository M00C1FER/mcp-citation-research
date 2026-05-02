package fetch

import (
"context"
"net"
"net/http"
"net/http/httptest"
"strings"
"sync/atomic"
"testing"
"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// validateURL
// ─────────────────────────────────────────────────────────────────────────────

func TestValidateURL_RejectsLoopback(t *testing.T) {
cases := []string{
"http://127.0.0.1/",
"http://127.1.2.3/path",
"http://[::1]/",
}
for _, u := range cases {
if err := validateURL(u); err == nil {
t.Errorf("validateURL(%q): expected error for loopback address, got nil", u)
}
}
}

func TestValidateURL_RejectsPrivateRanges(t *testing.T) {
cases := []string{
"http://10.0.0.1/",
"http://10.255.255.255/",
"http://192.168.0.1/",
"http://172.16.0.1/",
"http://172.31.255.255/",
"http://169.254.169.254/", // EC2 metadata endpoint
"http://100.64.0.1/",      // RFC 6598 carrier-grade NAT
}
for _, u := range cases {
if err := validateURL(u); err == nil {
t.Errorf("validateURL(%q): expected error for private IP, got nil", u)
}
}
}

func TestValidateURL_RejectsBadScheme(t *testing.T) {
cases := []string{
"ftp://example.com/file",
"file:///etc/passwd",
"gopher://example.com/",
"javascript:alert(1)",
}
for _, u := range cases {
if err := validateURL(u); err == nil {
t.Errorf("validateURL(%q): expected error for forbidden scheme, got nil", u)
}
}
}

func TestValidateURL_RejectsEmptyHost(t *testing.T) {
if err := validateURL("http:///path"); err == nil {
t.Error("validateURL: expected error for URL with empty host, got nil")
}
}

func TestValidateURL_RejectsMalformed(t *testing.T) {
if err := validateURL("not a url"); err == nil {
t.Error("validateURL: expected error for non-URL string, got nil")
}
}

// ─────────────────────────────────────────────────────────────────────────────
// isPrivateIP
// ─────────────────────────────────────────────────────────────────────────────

func TestIsPrivateIP_PrivateAddresses(t *testing.T) {
cases := []string{
"10.0.0.1",
"10.255.255.255",
"172.16.0.1",
"172.31.255.255",
"192.168.0.1",
"192.168.255.255",
"100.64.0.1",  // RFC 6598
"169.254.1.1", // link-local
"fd00::1",     // IPv6 unique-local
}
for _, addr := range cases {
ip := net.ParseIP(addr)
if ip == nil {
t.Fatalf("net.ParseIP(%q) returned nil", addr)
}
if !isPrivateIP(ip) {
t.Errorf("isPrivateIP(%q) = false, want true", addr)
}
}
}

func TestIsPrivateIP_PublicAddresses(t *testing.T) {
cases := []string{
"8.8.8.8",
"1.1.1.1",
"93.184.216.34",
"2001:4860:4860::8888", // Google Public DNS IPv6
}
for _, addr := range cases {
ip := net.ParseIP(addr)
if ip == nil {
t.Fatalf("net.ParseIP(%q) returned nil", addr)
}
if isPrivateIP(ip) {
t.Errorf("isPrivateIP(%q) = true, want false", addr)
}
}
}

// ─────────────────────────────────────────────────────────────────────────────
// extractHTML
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractHTML_TitleAndText(t *testing.T) {
const raw = `<html>
<head><title>Test Title</title></head>
<body>
  <script>var secret = 1;</script>
  <style>body { color: red; }</style>
  <noscript>no-script fallback</noscript>
  <p>Visible paragraph content.</p>
</body>
</html>`

title, text := extractHTML(strings.NewReader(raw))
if title != "Test Title" {
t.Errorf("title = %q, want %q", title, "Test Title")
}
if !strings.Contains(text, "Visible paragraph content") {
t.Errorf("text should contain body content; got %q", text)
}
for _, banned := range []string{"var secret", "color: red", "no-script fallback"} {
if strings.Contains(text, banned) {
t.Errorf("text should not contain %q (content from excluded tag)", banned)
}
}
}

func TestExtractHTML_Empty(t *testing.T) {
title, text := extractHTML(strings.NewReader(""))
if title != "" || text != "" {
t.Errorf("empty input: got title=%q text=%q, want both empty", title, text)
}
}

func TestExtractHTML_NoTitle(t *testing.T) {
const raw = `<html><body><p>just some text</p></body></html>`
title, text := extractHTML(strings.NewReader(raw))
if title != "" {
t.Errorf("expected empty title, got %q", title)
}
if !strings.Contains(text, "just some text") {
t.Errorf("expected body text, got %q", text)
}
}

// ─────────────────────────────────────────────────────────────────────────────
// Concurrent — SSRF protection and edge cases
//
// Note: httptest.NewServer binds to 127.0.0.1. Because validateURL rejects
// loopback addresses, these tests exercise the SSRF protection path: the
// handler is never reached and OK is always false.
// ─────────────────────────────────────────────────────────────────────────────

// TestConcurrent_SSRFBlocked verifies that Concurrent rejects loopback URLs
// before establishing any network connection. If the httptest handler fires,
// the SSRF guard has failed.
func TestConcurrent_SSRFBlocked(t *testing.T) {
reached := false
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
reached = true
w.WriteHeader(http.StatusOK)
}))
defer srv.Close()

pages := Concurrent(context.Background(), []string{srv.URL}, 1, 5*time.Second)
if len(pages) != 1 {
t.Fatalf("len(pages) = %d, want 1", len(pages))
}
if pages[0].OK {
t.Errorf("page.OK = true for loopback URL %q; SSRF protection must block this", srv.URL)
}
if pages[0].Error == "" {
t.Error("page.Error is empty; expected a non-empty rejection message")
}
if reached {
t.Error("SSRF protection failed: request reached the handler")
}
}

// TestConcurrent_BadScheme verifies that non-http(s) URLs are rejected.
func TestConcurrent_BadScheme(t *testing.T) {
pages := Concurrent(context.Background(), []string{"ftp://example.com/"}, 1, 5*time.Second)
if len(pages) != 1 {
t.Fatalf("len(pages) = %d, want 1", len(pages))
}
if pages[0].OK {
t.Error("expected OK=false for ftp:// URL, got OK=true")
}
}

// TestConcurrent_EmptyInput ensures a nil/empty URL slice returns no pages.
func TestConcurrent_EmptyInput(t *testing.T) {
pages := Concurrent(context.Background(), nil, 4, 5*time.Second)
if len(pages) != 0 {
t.Errorf("expected 0 pages for nil input, got %d", len(pages))
}
}

// TestConcurrent_NoPanicOnMixed ensures the runner completes without panic
// or deadlock for a mix of blocked URLs. All are SSRF/scheme violations so
// they return immediately without network I/O.
func TestConcurrent_NoPanicOnMixed(t *testing.T) {
urls := []string{
"http://127.0.0.1/blocked",
"http://192.168.0.1/blocked",
"ftp://bad-scheme/",
"http://10.0.0.1/blocked",
}

done := make(chan []Page, 1)
go func() {
done <- Concurrent(context.Background(), urls, 2, 200*time.Millisecond)
}()

select {
case pages := <-done:
if len(pages) != 4 {
t.Errorf("len(pages) = %d, want 4", len(pages))
}
for _, p := range pages {
if p.OK {
t.Errorf("expected OK=false for blocked URL %q, got OK=true", p.URL)
}
}
case <-time.After(5 * time.Second):
t.Fatal("Concurrent deadlocked: did not return within 5 seconds")
}
}

// TestConcurrent_CancelledContext verifies that a pre-cancelled context
// doesn't panic or deadlock. SSRF-blocked URLs return before any dial, so
// every page gets an error regardless of context state.
func TestConcurrent_CancelledContext(t *testing.T) {
ctx, cancel := context.WithCancel(context.Background())
cancel()

urls := []string{"http://127.0.0.1/", "http://10.0.0.1/"}
pages := Concurrent(ctx, urls, 2, 5*time.Second)
if len(pages) != 2 {
t.Fatalf("len(pages) = %d, want 2", len(pages))
}
for _, p := range pages {
if p.OK {
t.Errorf("expected OK=false for blocked URL %q with cancelled ctx", p.URL)
}
}
}

// TestConcurrent_PreservesOrder verifies that pages are returned in the same
// order as the input URL slice (no race-induced reordering).
func TestConcurrent_PreservesOrder(t *testing.T) {
urls := []string{
"http://127.0.0.1/first",
"http://127.0.0.2/second",
"http://127.0.0.3/third",
}
pages := Concurrent(context.Background(), urls, 3, 2*time.Second)
if len(pages) != len(urls) {
t.Fatalf("len(pages) = %d, want %d", len(pages), len(urls))
}
for i, p := range pages {
if p.URL != urls[i] {
t.Errorf("pages[%d].URL = %q, want %q", i, p.URL, urls[i])
}
}
}

// ─────────────────────────────────────────────────────────────────────────────
// isFetchTransient — unit tests
// ─────────────────────────────────────────────────────────────────────────────

func TestIsFetchTransient_TransientCodes(t *testing.T) {
transient := []int{
http.StatusTooManyRequests,   // 429
http.StatusInternalServerError, // 500
http.StatusBadGateway,          // 502
http.StatusServiceUnavailable,  // 503
http.StatusGatewayTimeout,      // 504
}
for _, code := range transient {
if !isFetchTransient(code) {
t.Errorf("isFetchTransient(%d) = false, want true", code)
}
}
}

func TestIsFetchTransient_PermanentCodes(t *testing.T) {
permanent := []int{
http.StatusOK,          // 200
http.StatusBadRequest,  // 400
http.StatusUnauthorized, // 401
http.StatusForbidden,   // 403
http.StatusNotFound,    // 404
}
for _, code := range permanent {
if isFetchTransient(code) {
t.Errorf("isFetchTransient(%d) = true, want false", code)
}
}
}

// ─────────────────────────────────────────────────────────────────────────────
// fetchAttempt — retry signal tests (package-internal)
// ─────────────────────────────────────────────────────────────────────────────

// TestFetchAttempt_502SignalsRetry verifies that a 502 response causes
// fetchAttempt to return a non-zero retryDelay.
func TestFetchAttempt_502SignalsRetry(t *testing.T) {
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
http.Error(w, "bad gateway", http.StatusBadGateway)
}))
defer srv.Close()

_, retryDelay := fetchAttempt(context.Background(), srv.URL, 5*time.Second, 0)
if retryDelay == 0 {
t.Error("fetchAttempt: expected non-zero retryDelay for 502, got 0")
}
}

// TestFetchAttempt_404DoesNotRetry verifies that a 404 response (permanent
// client error) causes fetchAttempt to return retryDelay == 0.
func TestFetchAttempt_404DoesNotRetry(t *testing.T) {
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
http.Error(w, "not found", http.StatusNotFound)
}))
defer srv.Close()

_, retryDelay := fetchAttempt(context.Background(), srv.URL, 5*time.Second, 0)
if retryDelay != 0 {
t.Errorf("fetchAttempt: expected retryDelay==0 for 404, got %v", retryDelay)
}
}

// TestFetchAttempt_RetryAfterParsed verifies that a Retry-After header on a 429
// response is parsed and returned as the retryDelay (capped at 10 s).
func TestFetchAttempt_RetryAfterParsed(t *testing.T) {
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.Header().Set("Retry-After", "2")
http.Error(w, "too many requests", http.StatusTooManyRequests)
}))
defer srv.Close()

_, retryDelay := fetchAttempt(context.Background(), srv.URL, 5*time.Second, 0)
if retryDelay != 2*time.Second {
t.Errorf("fetchAttempt: expected retryDelay=2s from Retry-After header, got %v", retryDelay)
}
}

// TestFetchAttempt_RetryAfterCapped verifies that a very large Retry-After
// value is capped at 10 s to prevent runaway sleeps.
func TestFetchAttempt_RetryAfterCapped(t *testing.T) {
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.Header().Set("Retry-After", "3600") // 1 hour — must be capped
http.Error(w, "too many requests", http.StatusTooManyRequests)
}))
defer srv.Close()

_, retryDelay := fetchAttempt(context.Background(), srv.URL, 5*time.Second, 0)
if retryDelay > 10*time.Second {
t.Errorf("fetchAttempt: retryDelay %v exceeds 10 s cap", retryDelay)
}
}

// TestFetchAttempt_SuccessNoRetry verifies that a 200 OK response returns
// retryDelay == 0 (no retry needed).
func TestFetchAttempt_SuccessNoRetry(t *testing.T) {
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.Header().Set("Content-Type", "text/html")
_, _ = w.Write([]byte("<html><head><title>Test</title></head><body>Hello</body></html>"))
}))
defer srv.Close()

page, retryDelay := fetchAttempt(context.Background(), srv.URL, 5*time.Second, 0)
if retryDelay != 0 {
t.Errorf("fetchAttempt: expected no retry for 200 OK, got retryDelay=%v", retryDelay)
}
if !page.OK {
t.Errorf("fetchAttempt: expected OK=true for 200 response, got OK=false (err: %s)", page.Error)
}
}

// TestFetchAttempt_503RetryWithSuccessOnSecond exercises the full retry loop
// via fetchOne (indirectly): first request returns 503, second returns 200.
// Note: fetchOne validates URLs so we can only test via fetchAttempt directly.
func TestFetchAttempt_503RetryCount(t *testing.T) {
var calls atomic.Int32
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
n := calls.Add(1)
if n < 2 {
http.Error(w, "unavailable", http.StatusServiceUnavailable)
return
}
w.Header().Set("Content-Type", "text/html")
_, _ = w.Write([]byte("<html><head><title>OK</title></head><body>Done</body></html>"))
}))
defer srv.Close()

// Call fetchAttempt twice (simulating the retry loop) to verify that
// the second attempt succeeds.
_, retry1 := fetchAttempt(context.Background(), srv.URL, 5*time.Second, 0)
if retry1 == 0 {
t.Fatal("first attempt (503): expected retry signal, got none")
}
page2, retry2 := fetchAttempt(context.Background(), srv.URL, 5*time.Second, 1)
if retry2 != 0 {
t.Errorf("second attempt (200): expected no retry, got retryDelay=%v", retry2)
}
if !page2.OK {
t.Errorf("second attempt: expected OK=true, got OK=false (err: %s)", page2.Error)
}
}
