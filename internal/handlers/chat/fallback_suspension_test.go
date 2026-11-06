package chat

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/resilience"
)

// suspendedBody is how an upstream delivers a verdict on the account itself,
// as opposed to a throttle or a dead credential.
const suspendedBody = `{"error":{"code":403,"message":"This account has been suspended for violating the Terms of Service.","status":"PERMISSION_DENIED"}}`

// TestAccountSuspensionHaltsProviderAfterOneAccount is the regression for the
// cascade: one suspended account used to cool down and rotate, so every
// remaining account was spent on the same verdict and each needed its own
// appeal. Exactly one upstream request may leave the gateway.
func TestAccountSuspensionHaltsProviderAfterOneAccount(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(suspendedBody))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	for i := 1; i <= 3; i++ {
		connID := "conn-susp-" + strconv.Itoa(i)
		data, _ := json.Marshal(map[string]any{"apiKey": "sk-susp-" + strconv.Itoa(i), "baseUrl": srv.URL})
		if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
			VALUES (?, 'deepseek', 'apikey', 'Pooled', ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			connID, i, string(data)); err != nil {
			t.Fatalf("seed connection %s: %v", connID, err)
		}
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	err := h.handleAccountFallback(context.Background(), rec, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions")
	if err == nil {
		t.Fatal("handleAccountFallback() = nil error on a suspension, want the upstream error surfaced")
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("upstream received %d requests, want 1: the remaining accounts must not be spent on the same verdict", got)
	}
	if !resilience.IsProviderSuspended("deepseek") {
		t.Error("IsProviderSuspended(deepseek) = false, want true: the provider must be latched")
	}
}

// A throttle must keep the old behaviour. This is the false-positive guard at
// the integration level: if the detection widens, one rate-limited account
// would halt the provider and this test catches it.
func TestRateLimitStillRotatesAndDoesNotHalt(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	const okBody = `{"id":"chatcmpl-1","object":"chat.completion","created":0,"model":"deepseek-chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	for i := 1; i <= 2; i++ {
		connID := "conn-rl-" + strconv.Itoa(i)
		data, _ := json.Marshal(map[string]any{"apiKey": "sk-rl-" + strconv.Itoa(i), "baseUrl": srv.URL})
		if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
			VALUES (?, 'deepseek', 'apikey', 'Pooled', ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			connID, i, string(data)); err != nil {
			t.Fatalf("seed connection %s: %v", connID, err)
		}
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	if err := h.handleAccountFallback(context.Background(), rec, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err != nil {
		t.Fatalf("handleAccountFallback() = %v, want the second account to succeed", err)
	}

	if got := hits.Load(); got != 2 {
		t.Errorf("upstream received %d requests, want 2: a throttled account must still rotate", got)
	}
	if resilience.IsProviderSuspended("deepseek") {
		t.Error("IsProviderSuspended(deepseek) = true after a 429, want false: a throttle must never halt the provider")
	}
}

// Once the provider is halted, the NEXT request must not reach the upstream at
// all: admission is refused before any dial. Without this the latch would only
// stop the rotation inside one request, and a client retrying in a loop would
// keep spending accounts.
func TestHaltedProviderRefusesAdmissionBeforeDialling(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(suspendedBody))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	for i := 1; i <= 3; i++ {
		connID := "conn-gate-" + strconv.Itoa(i)
		data, _ := json.Marshal(map[string]any{"apiKey": "sk-gate-" + strconv.Itoa(i), "baseUrl": srv.URL})
		if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
			VALUES (?, 'deepseek', 'apikey', 'Pooled', ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			connID, i, string(data)); err != nil {
			t.Fatalf("seed connection %s: %v", connID, err)
		}
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)

	if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err == nil {
		t.Fatal("first request returned no error on a suspension")
	}
	first := hits.Load()
	if first != 1 {
		t.Fatalf("first request spent %d accounts, want 1", first)
	}

	for range 5 {
		_ = h.handleAccountFallback(context.Background(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions")
	}

	if got := hits.Load(); got != first {
		t.Errorf("upstream received %d requests after the halt, want %d: a halted provider must be refused before any dial", got, first)
	}
}

// An unrelated provider must keep working while one is halted.
func TestHaltIsScopedToTheOffendingProvider(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	const okBody = `{"id":"chatcmpl-1","object":"chat.completion","created":0,"model":"llama-3-70b","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(okBody))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	data, _ := json.Marshal(map[string]any{"apiKey": "sk-groq", "baseUrl": srv.URL})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
		VALUES ('conn-groq', 'groq', 'apikey', 'Groq', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		string(data)); err != nil {
		t.Fatalf("seed groq connection: %v", err)
	}

	resilience.RecordAccountSuspension("deepseek", "conn-susp-1", http.StatusForbidden, "account suspended")
	if !resilience.IsProviderSuspended("deepseek") {
		t.Fatal("deepseek was not halted, so this test proves nothing")
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	body := []byte(`{"model":"llama-3-70b","messages":[{"role":"user","content":"hi"}]}`)
	if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(), "groq", "llama-3-70b", "", body, false, false, "/v1/chat/completions"); err != nil {
		t.Fatalf("groq request failed while deepseek was halted: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("groq upstream received %d requests, want 1: an unrelated provider must keep serving", got)
	}
}

// The combo loop dispatches tryForwardWithConnection directly instead of going
// through handleAccountFallback, so it needs the halt enforced on its own path:
// a combo was the cheapest way to spend the whole pool on one verdict.
func TestComboLoopHaltsOnAccountSuspension(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(suspendedBody))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	for i := 1; i <= 3; i++ {
		connID := "conn-combo-" + strconv.Itoa(i)
		data, _ := json.Marshal(map[string]any{"apiKey": "sk-combo-" + strconv.Itoa(i), "baseUrl": srv.URL})
		if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
			VALUES (?, 'deepseek', 'apikey', 'Pooled', ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			connID, i, string(data)); err != nil {
			t.Fatalf("seed connection %s: %v", connID, err)
		}
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	comboModels := []string{"deepseek/deepseek-chat"}
	modelsJSON, _ := json.Marshal(comboModels)
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES ('combo-susp', 'combo-susp', 'fallback', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(modelsJSON)); err != nil {
		t.Fatalf("seed combo: %v", err)
	}

	translatedReq := map[string]any{
		"model":      "deepseek-chat",
		"max_tokens": 100,
		"messages":   []map[string]any{{"role": "user", "content": "hi"}},
	}
	rec := httptest.NewRecorder()
	h.handleMessagesComboFallback(context.Background(), newCommittedResponseWriter(rec), translatedReq, comboModels, "fallback", false, "combo-susp", 0)

	if got := hits.Load(); got != 1 {
		t.Errorf("combo spent %d accounts on one suspension, want 1", got)
	}
	if !resilience.IsProviderSuspended("deepseek") {
		t.Error("IsProviderSuspended(deepseek) = false after a combo suspension, want true")
	}
}

// The operator's expectation, from the dashboard's own vocabulary: a suspended
// account must switch the provider's toggle off, because that toggle (isActive)
// is where an operator looks. A latch that only lived in memory left the card
// showing the provider as enabled while every request was refused.
func TestSuspensionSwitchesTheProviderToggleOff(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(suspendedBody))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "deepseek", 5, srv.URL)
	// A second provider must keep its toggle on.
	seedConnDB(t, database, "groq", "conn-groq-keep", "sk-keep", srv.URL)

	activeFor := func(provider string) int {
		t.Helper()
		var n int
		if err := database.QueryRow(
			`SELECT COUNT(*) FROM providerConnections WHERE provider = ? AND isActive = 1`, provider).Scan(&n); err != nil {
			t.Fatalf("count active %s: %v", provider, err)
		}
		return n
	}

	if got := activeFor("deepseek"); got != 5 {
		t.Fatalf("baseline has %d active deepseek connections, want 5", got)
	}

	h := NewChatHandler(db.NewRepo(database))
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	if err := h.handleAccountFallback(t.Context(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err == nil {
		t.Fatal("handleAccountFallback returned no error on a suspension")
	}

	if !resilience.IsProviderSuspended("deepseek") {
		t.Error("provider was not halted")
	}
	if got := activeFor("deepseek"); got != 0 {
		t.Errorf("deepseek still has %d active connections, want 0: the dashboard toggle must read off", got)
	}
	if got := activeFor("groq"); got != 1 {
		t.Errorf("groq has %d active connections, want 1: an unrelated provider must keep its toggle on", got)
	}
}

// Below the threshold the provider is still serving, so its accounts must stay
// on: switching them off early would halt a provider the gateway has not
// decided to halt.
func TestSuspensionBelowThresholdLeavesTheToggleOn(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()
	resilience.SetSuspensionThreshold(3)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(suspendedBody))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "deepseek", 4, srv.URL)

	h := NewChatHandler(db.NewRepo(database))
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	_ = h.handleAccountFallback(t.Context(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions")

	if resilience.IsProviderSuspended("deepseek") {
		t.Fatal("one account reached a threshold of three")
	}
	var active int
	if err := database.QueryRow(`SELECT COUNT(*) FROM providerConnections WHERE provider = 'deepseek' AND isActive = 1`).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 4 {
		t.Errorf("%d of 4 connections are still on, want 4: a provider below the threshold must keep serving", active)
	}
}
