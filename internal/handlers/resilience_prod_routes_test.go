package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"

	"github.com/go-chi/chi/v5"
)

// TestSetupServerRouter_ResilienceRoutesAreLive pins the resilience endpoints to
// the router the server actually mounts, not the tests-only
// dashboard.RegisterRoutes table.
//
// All three lived in that tests-only table alone, which made the provider halt
// one-way on a real server: it could latch, but the only thing that releases it
// answered 404 — while its own refusal message told the operator to "clear the
// halt after appealing". The latch is persisted, so a restart did not release
// it either, and an operator who followed the message had no way out.
//
// Same failure shape as the jailbreak and codex reset-credit routes above, which
// is why the guard is the same: boot SetupServerRouter directly and assert no
// resilience path falls through to chi's not-found handler.
func TestSetupServerRouter_ResilienceRoutesAreLive(t *testing.T) {
	t.Setenv("JWT_SECRET", "resilience-router-test-secret")
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	token, err := auth.Sign("resilience-router-test-secret", time.Now())
	if err != nil {
		t.Fatalf("sign session token: %v", err)
	}
	authed := func(method, path, body string) *httptest.ResponseRecorder {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/resilience", ""},
		{http.MethodPost, "/api/resilience/reset", `{}`},
		// The one that matters most: releasing a halt. A 404 here means a
		// halted provider can never be resumed on a real server.
		{http.MethodPost, "/api/resilience/suspensions/clear", `{"provider":"antigravity"}`},
	} {
		rec := authed(tc.method, tc.path, tc.body)
		// A missing route and a handler's own 404 both answer 404, so the body
		// is what separates them: chi's not-found writes plain text, while the
		// clear handler legitimately answers 404 "provider is not halted" when
		// nothing is latched — which is the state of a fresh test database.
		if isChiNotFound(rec) {
			t.Errorf("%s %s is not registered on the production router: %s",
				tc.method, tc.path, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// isChiNotFound reports whether the response came from chi's not-found handler
// rather than from a route that ran.
func isChiNotFound(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusNotFound &&
		strings.Contains(rec.Body.String(), "404 page not found")
}
