package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/resilience"
)

func decodeResilience(t *testing.T, rec *httptest.ResponseRecorder) ResilienceStatusResponse {
	t.Helper()
	var resp ResilienceStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}
	return resp
}

// A halt never clears on its own, so an operator who cannot see it in the panel
// has no way to tell a deliberate halt apart from an upstream outage.
func TestGetResilienceSurfacesHaltedProvider(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	h := &DashboardHandler{}

	rec := httptest.NewRecorder()
	h.HandleGetResilience(rec, httptest.NewRequest(http.MethodGet, "/api/resilience", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := decodeResilience(t, rec); len(got.Suspensions) != 0 {
		t.Fatalf("fresh gateway reported %d halts, want 0", len(got.Suspensions))
	}

	resilience.RecordAccountSuspension("antigravity", "conn-1", http.StatusForbidden, "account suspended")

	rec = httptest.NewRecorder()
	h.HandleGetResilience(rec, httptest.NewRequest(http.MethodGet, "/api/resilience", nil))
	resp := decodeResilience(t, rec)
	if len(resp.Suspensions) != 1 {
		t.Fatalf("reported %d halts, want 1: %+v", len(resp.Suspensions), resp.Suspensions)
	}
	got := resp.Suspensions[0]
	if got.Provider != "antigravity" {
		t.Errorf("provider = %q, want antigravity", got.Provider)
	}
	if got.StatusCode != http.StatusForbidden {
		t.Errorf("statusCode = %d, want 403", got.StatusCode)
	}
	if got.Reason != "account suspended" {
		t.Errorf("reason = %q, want %q", got.Reason, "account suspended")
	}
	if len(got.Accounts) != 1 || got.Accounts[0] != "conn-1" {
		t.Errorf("accounts = %v, want [conn-1]: the operator needs to know which account to appeal", got.Accounts)
	}
	if got.FirstSeenAt == "" {
		t.Error("firstSeenAt is empty, so the panel cannot say how long the halt has been in place")
	}
}

func TestClearSuspensionReleasesTheHalt(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	h := &DashboardHandler{}
	resilience.RecordAccountSuspension("antigravity", "conn-1", http.StatusForbidden, "account suspended")
	if !resilience.IsProviderSuspended("antigravity") {
		t.Fatal("provider was not halted, so this test proves nothing")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/resilience/suspensions/clear", strings.NewReader(`{"provider":"antigravity"}`))
	rec := httptest.NewRecorder()
	h.HandleClearSuspension(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if resilience.IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended = true after the clear, want false")
	}
}

// An alias must reach the same latch the request path recorded, or an operator
// clearing "ag" would be told the halt does not exist while traffic to
// antigravity stays blocked.
func TestClearSuspensionResolvesProviderAlias(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	h := &DashboardHandler{}
	resilience.RecordAccountSuspension("antigravity", "conn-1", http.StatusForbidden, "account suspended")

	req := httptest.NewRequest(http.MethodPost, "/api/resilience/suspensions/clear", strings.NewReader(`{"provider":"ag"}`))
	rec := httptest.NewRecorder()
	h.HandleClearSuspension(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if resilience.IsProviderSuspended("antigravity") {
		t.Error("clearing the alias left the canonical provider halted")
	}
}

func TestClearSuspensionRejectsBadRequests(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	h := &DashboardHandler{}

	tests := []struct {
		name string
		body string
		want int
	}{
		{"malformed json", `{`, http.StatusBadRequest},
		{"missing provider", `{}`, http.StatusBadRequest},
		{"blank provider", `{"provider":"   "}`, http.StatusBadRequest},
		{"provider is not halted", `{"provider":"antigravity"}`, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/resilience/suspensions/clear", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.HandleClearSuspension(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

// The reset button closes breakers an operator has already fixed. Folding the
// halt into it would let a routine click resume the cascade the latch exists to
// stop, so the two must stay separate.
func TestResetResilienceDoesNotReleaseAHalt(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	h := &DashboardHandler{Resilience: resilience.DefaultController()}
	resilience.RecordAccountSuspension("antigravity", "conn-1", http.StatusForbidden, "account suspended")

	rec := httptest.NewRecorder()
	h.HandleResetResilience(rec, httptest.NewRequest(http.MethodPost, "/api/resilience/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if !resilience.IsProviderSuspended("antigravity") {
		t.Error("the reset endpoint released a provider halt: only an explicit clear may do that")
	}
}
