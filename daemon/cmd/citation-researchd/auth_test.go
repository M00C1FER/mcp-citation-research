package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestAuthMiddleware_RejectsMissingHeader(t *testing.T) {
	mw := authMiddleware("secret", okHandler())
	r := httptest.NewRequest("POST", "/search", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing header: want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_RejectsWrongToken(t *testing.T) {
	mw := authMiddleware("secret", okHandler())
	r := httptest.NewRequest("POST", "/search", nil)
	r.Header.Set("Authorization", "Bearer not-the-token")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_AcceptsCorrectToken(t *testing.T) {
	mw := authMiddleware("secret", okHandler())
	r := httptest.NewRequest("POST", "/search", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("correct token: want 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_HealthzExemptFromAuth(t *testing.T) {
	mw := authMiddleware("secret", okHandler())
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz should be exempt: want 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_NoTokenDisablesAuth(t *testing.T) {
	mw := authMiddleware("", okHandler())
	r := httptest.NewRequest("POST", "/search", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("no token configured = open mode: want 200, got %d", w.Code)
	}
}
