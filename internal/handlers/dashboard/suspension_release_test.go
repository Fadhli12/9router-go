package dashboard

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/resilience"
)

func putConnectionActive(t *testing.T, router http.Handler, connID string, active bool) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"isActive": false}`
	if active {
		body = `{"isActive": true}`
	}
	req := httptest.NewRequest(http.MethodPut, "/api/connections/"+connID, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// The halt switches every connection off, so the operator's way back is the
// same per-provider toggle they already use. Without this release the toggle is
// a no-op: isActive flips back to 1, the card looks enabled, and the resolver
// keeps refusing because the latch is independent of isActive.
func TestEnablingAConnectionClearsTheProviderHalt(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	connID := "conn-halted"
	if err := repo.CreateProviderConnection(connID, "antigravity", "apikey", "Halted", "sk-1"); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	resilience.RecordAccountSuspension("antigravity", connID, http.StatusForbidden, "account suspended")
	if _, err := repo.DeactivateProviderConnections("antigravity"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if !resilience.IsProviderSuspended("antigravity") {
		t.Fatal("provider is not halted, so this test proves nothing")
	}

	if rec := putConnectionActive(t, router, connID, true); rec.Code != http.StatusOK {
		t.Fatalf("enable returned %d: %s", rec.Code, rec.Body.String())
	}

	if resilience.IsProviderSuspended("antigravity") {
		t.Error("the provider is still halted after re-enabling an account from the dashboard")
	}
	conn, err := repo.GetProviderConnectionByID(connID)
	if err != nil || conn == nil {
		t.Fatalf("reload connection: %v", err)
	}
	if conn.IsActive != 1 {
		t.Errorf("isActive = %d, want 1", conn.IsActive)
	}
}

// Turning accounts off is not a claim that the appeal succeeded, so a disable
// must leave the latch in place.
func TestDisablingAConnectionLeavesTheHaltInPlace(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	connID := "conn-still-halted"
	if err := repo.CreateProviderConnection(connID, "antigravity", "apikey", "Halted", "sk-1"); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	resilience.RecordAccountSuspension("antigravity", connID, http.StatusForbidden, "account suspended")

	if rec := putConnectionActive(t, router, connID, false); rec.Code != http.StatusOK {
		t.Fatalf("disable returned %d: %s", rec.Code, rec.Body.String())
	}

	if !resilience.IsProviderSuspended("antigravity") {
		t.Error("disabling an account released the halt; only an enable may do that")
	}
}

// Re-enabling one provider's account must not release a different provider's
// halt: two providers halted for different reasons need two separate appeals.
func TestEnablingOneProviderDoesNotClearAnothersHalt(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	if err := repo.CreateProviderConnection("conn-ag", "antigravity", "apikey", "AG", "sk-ag"); err != nil {
		t.Fatalf("create antigravity connection: %v", err)
	}
	if err := repo.CreateProviderConnection("conn-oc", "opencode", "apikey", "OC", "sk-oc"); err != nil {
		t.Fatalf("create opencode connection: %v", err)
	}

	resilience.RecordAccountSuspension("antigravity", "conn-ag", http.StatusForbidden, "account suspended")
	resilience.RecordAccountSuspension("opencode", "conn-oc", http.StatusForbidden, "account suspended")

	if rec := putConnectionActive(t, router, "conn-ag", true); rec.Code != http.StatusOK {
		t.Fatalf("enable returned %d: %s", rec.Code, rec.Body.String())
	}

	if resilience.IsProviderSuspended("antigravity") {
		t.Error("antigravity is still halted after its account was re-enabled")
	}
	if !resilience.IsProviderSuspended("opencode") {
		t.Error("opencode's halt was released by an unrelated provider's toggle")
	}
}

// A drift halt is not an account suspension: its exit is patching the pinned
// identity, not appealing a credential. Enabling an account into it is refused
// with the drift reason, so the toggle cannot be mistaken for the release.
func TestEnablingIntoADriftHaltIsRefused(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	connID := "conn-drifted"
	if err := repo.CreateProviderConnection(connID, "antigravity", "apikey", "Drifted", "sk-1"); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	reason := resilience.DriftHaltReasonPrefix + "Antigravity released 1.5.4, this gateway sends 1.5.3"
	resilience.RecordAccountSuspension("antigravity", "(no account: client version drift)", 0, reason)
	if _, err := repo.DeactivateProviderConnections("antigravity"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if !resilience.IsProviderSuspended("antigravity") {
		t.Fatal("provider is not halted, so this test proves nothing")
	}

	rec := putConnectionActive(t, router, connID, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("enable into a drift halt returned %d: %s", rec.Code, rec.Body.String())
	}
	if !resilience.IsProviderSuspended("antigravity") {
		t.Error("the drift halt was released by a connection enable; only patching the identity may do that")
	}
	conn, err := repo.GetProviderConnectionByID(connID)
	if err != nil || conn == nil {
		t.Fatalf("reload connection: %v", err)
	}
	if conn.IsActive != 0 {
		t.Errorf("isActive = %d, want 0: the refused enable must not flip the row", conn.IsActive)
	}
}
