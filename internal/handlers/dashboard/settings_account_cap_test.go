package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The cap is only useful if an operator can set it, so this pins the one path
// the dashboard uses: PATCH /api/settings must store maxActiveAccounts inside
// providerStrategies and GET must echo it back.
func TestMaxActiveAccountsRoundTripsThroughSettings(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := &DashboardHandler{Repo: repo}

	body := `{"providerStrategies":{"antigravity":{"fallbackStrategy":"round-robin","maxActiveAccounts":10}}}`
	rec := httptest.NewRecorder()
	h.HandleUpdateSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleGetSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	strategies, ok := got["providerStrategies"].(map[string]any)
	if !ok {
		t.Fatalf("providerStrategies missing from the response: %s", rec.Body.String())
	}
	ag, ok := strategies["antigravity"].(map[string]any)
	if !ok {
		t.Fatalf("antigravity strategy missing: %+v", strategies)
	}
	cap, ok := ag["maxActiveAccounts"].(float64)
	if !ok {
		t.Fatalf("maxActiveAccounts missing or not a number: %+v", ag)
	}
	if int(cap) != 10 {
		t.Errorf("maxActiveAccounts = %d, want 10", int(cap))
	}

	// The cap must reach the typed read path the selector consults, not just
	// the raw JSON the dashboard echoes.
	settings, err := repo.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if settings.ProviderStrategies["antigravity"].MaxActiveAccounts != 10 {
		t.Errorf("typed MaxActiveAccounts = %d, want 10",
			settings.ProviderStrategies["antigravity"].MaxActiveAccounts)
	}
}

// Setting the cap must not drop the rotation strategy stored beside it: both
// live in the same per-provider object, so a careless write loses one.
func TestMaxActiveAccountsPreservesSiblingStrategyFields(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := &DashboardHandler{Repo: repo}

	body := `{"providerStrategies":{"antigravity":{"fallbackStrategy":"round-robin","stickyRoundRobinLimit":3,"maxActiveAccounts":5}}}`
	rec := httptest.NewRecorder()
	h.HandleUpdateSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	settings, err := repo.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	strat := settings.ProviderStrategies["antigravity"]
	if strat.MaxActiveAccounts != 5 {
		t.Errorf("MaxActiveAccounts = %d, want 5", strat.MaxActiveAccounts)
	}
	if strat.RotateStrategy != "round-robin" {
		t.Errorf("RotateStrategy = %q, want round-robin: the cap write dropped a sibling field", strat.RotateStrategy)
	}
	if strat.StickyLimit != 3 {
		t.Errorf("StickyLimit = %d, want 3", strat.StickyLimit)
	}
}

// An absent or zero cap must read as unlimited, which is what every existing
// install carries.
func TestMaxActiveAccountsAbsentReadsAsUnlimited(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := &DashboardHandler{Repo: repo}

	body := `{"providerStrategies":{"antigravity":{"fallbackStrategy":"round-robin"}}}`
	rec := httptest.NewRecorder()
	h.HandleUpdateSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	settings, err := repo.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got := settings.ProviderStrategies["antigravity"].MaxActiveAccounts; got != 0 {
		t.Errorf("MaxActiveAccounts = %d with no cap sent, want 0 (unlimited)", got)
	}
}

// A negative cap must be ignored rather than clamped to zero: zero already
// means unlimited, so clamping would turn a typo into "expose every account".
func TestMaxActiveAccountsNegativeIsIgnored(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := &DashboardHandler{Repo: repo}

	body := `{"providerStrategies":{"antigravity":{"maxActiveAccounts":-3}}}`
	rec := httptest.NewRecorder()
	h.HandleUpdateSettings(rec, httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	settings, err := repo.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got := settings.ProviderStrategies["antigravity"].MaxActiveAccounts; got != 0 {
		t.Errorf("MaxActiveAccounts = %d for a negative input, want 0", got)
	}
}
