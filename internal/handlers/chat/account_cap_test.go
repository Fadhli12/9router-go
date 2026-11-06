package chat

import (
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/models"
	"9router/proxy/internal/resilience"
)

func seedCappedPool(t *testing.T, database *sql.DB, provider string, n int, baseURL string) {
	t.Helper()
	for i := 1; i <= n; i++ {
		connID := "conn-cap-" + strconv.Itoa(i)
		data, _ := json.Marshal(map[string]any{"apiKey": "sk-cap-" + strconv.Itoa(i), "baseUrl": baseURL})
		if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
			VALUES (?, ?, 'apikey', 'Pooled', ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			connID, provider, i, string(data)); err != nil {
			t.Fatalf("seed connection %s: %v", connID, err)
		}
	}
}

func seedMaxActiveAccounts(t *testing.T, database *sql.DB, provider string, cap int) {
	t.Helper()
	settings, _ := json.Marshal(map[string]any{
		"providerStrategies": map[string]any{
			provider: map[string]any{
				"fallbackStrategy":  "round-robin",
				"maxActiveAccounts": cap,
			},
		},
	})
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, string(settings)); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
}

// The cap exists so an upstream that punishes a shared trait reaches a bounded
// number of accounts. A pool of 40 behind a cap of 10 must therefore never
// present more than 10 identities, however many requests are made.
func TestAccountCapBoundsTheExposedIdentities(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "deepseek", 40, "https://mock.example.com")
	seedMaxActiveAccounts(t, database, "deepseek", 10)

	h := NewChatHandler(db.NewRepo(database))

	seen := map[string]bool{}
	for range 200 {
		conn, _, err := h.GetBestConnection("deepseek", "", nil, "deepseek-chat")
		if err != nil {
			t.Fatalf("GetBestConnection: %v", err)
		}
		seen[conn.ID] = true
	}

	if len(seen) > 10 {
		t.Errorf("reached %d distinct accounts, want at most 10: the cap must bound exposure", len(seen))
	}
	if len(seen) == 0 {
		t.Fatal("no account was selected at all")
	}
	for id := range seen {
		if id == "conn-cap-40" {
			t.Error("a standby account was dialled")
		}
	}
}

func TestAccountCapAbsentLeavesRotationUnchanged(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "deepseek", 12, "https://mock.example.com")

	settings, _ := json.Marshal(map[string]any{
		"providerStrategies": map[string]any{
			"deepseek": map[string]any{"fallbackStrategy": "round-robin"},
		},
	})
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, string(settings)); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	h := NewChatHandler(db.NewRepo(database))

	seen := map[string]bool{}
	for range 200 {
		conn, _, err := h.GetBestConnection("deepseek", "", nil, "deepseek-chat")
		if err != nil {
			t.Fatalf("GetBestConnection: %v", err)
		}
		seen[conn.ID] = true
	}

	if len(seen) <= 10 {
		t.Errorf("rotation reached only %d of 12 accounts with no cap configured, want more than 10", len(seen))
	}
}

// Auto-toggle: an account parked by a cooldown must hand its slot to a standby
// on the next request rather than shrinking the usable set.
func TestAccountCapPromotesStandbyWhenAnActiveAccountIsParked(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "deepseek", 40, "https://mock.example.com")
	seedMaxActiveAccounts(t, database, "deepseek", 3)

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	active := map[string]bool{}
	for range 60 {
		conn, _, err := h.GetBestConnection("deepseek", "", nil, "deepseek-chat")
		if err != nil {
			t.Fatalf("GetBestConnection: %v", err)
		}
		active[conn.ID] = true
	}
	if len(active) != 3 {
		t.Fatalf("baseline reached %d accounts, want 3: %v", len(active), active)
	}
	if !active["conn-cap-1"] || !active["conn-cap-2"] || !active["conn-cap-3"] {
		t.Fatalf("baseline set is not the first three by priority: %v", active)
	}

	if err := repo.RecordConnectionScopedError("conn-cap-2", "deepseek-chat", "chat", http.StatusTooManyRequests, "rate limited", 1); err != nil {
		t.Fatalf("park conn-cap-2: %v", err)
	}
	if err := repo.LockConnectionModel("conn-cap-2", "deepseek-chat", 3600, 1); err != nil {
		t.Fatalf("lock conn-cap-2: %v", err)
	}

	after := map[string]bool{}
	for range 60 {
		conn, _, err := h.GetBestConnection("deepseek", "", nil, "deepseek-chat")
		if err != nil {
			t.Fatalf("GetBestConnection after parking: %v", err)
		}
		after[conn.ID] = true
	}

	if after["conn-cap-2"] {
		t.Error("the parked account is still being dialled")
	}
	if !after["conn-cap-4"] {
		t.Errorf("conn-cap-4 was not promoted into the freed slot: %v", after)
	}
	if len(after) > 3 {
		t.Errorf("reached %d accounts after promotion, want at most 3: %v", len(after), after)
	}
}

// A suspension halts the provider outright, so promotion must not run: the
// standby accounts are exactly what the halt is protecting.
func TestAccountCapDoesNotPromoteIntoASuspension(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	var mu sync.Mutex
	identities := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		identities[r.Header.Get("Authorization")] = true
		mu.Unlock()
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
	seedCappedPool(t, database, "deepseek", 40, srv.URL)
	seedMaxActiveAccounts(t, database, "deepseek", 10)

	h := NewChatHandler(db.NewRepo(database))
	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)

	for range 5 {
		_ = h.handleAccountFallback(t.Context(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions")
	}

	mu.Lock()
	reached := len(identities)
	mu.Unlock()

	if reached != 1 {
		t.Errorf("upstream saw %d identities, want 1: the halt must stop promotion as well as rotation", reached)
	}
	if !resilience.IsProviderSuspended("deepseek") {
		t.Error("provider was not halted")
	}
}

// Regression for the gap the self-review found: the media handlers (image,
// search, STT) rotate accounts through GetBestConnectionWithContext with their
// own excludeIDs loops and never reach handleAccountFallback, so a gate placed
// only in the chat failover left ten call sites still spending accounts on a
// verdict already recorded. The resolver is the single door they all share.
func TestHaltedProviderRefusesAtTheResolver(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "antigravity", 3, "https://mock.example.com")

	h := NewChatHandler(db.NewRepo(database))

	if _, _, err := h.GetBestConnection("antigravity", "", nil, "gemini-3.8-flash"); err != nil {
		t.Fatalf("resolver refused before any halt: %v", err)
	}

	resilience.RecordAccountSuspension("antigravity", "conn-cap-1", http.StatusForbidden, "account suspended")

	// This is the call every media handler makes, including with the exclude
	// list they build as accounts fail.
	excludeIDs := []string{}
	for range 5 {
		conn, _, err := h.GetBestConnectionWithContext(t.Context(), "antigravity", "", excludeIDs, "gemini-3.8-flash")
		if err == nil {
			t.Fatalf("resolver handed out %s while the provider was halted", conn.ID)
		}
		if !strings.Contains(err.Error(), "halted") {
			t.Errorf("error = %q, want it to name the halt so the operator can act", err.Error())
		}
		if conn != nil {
			excludeIDs = append(excludeIDs, conn.ID)
		}
	}
}

// The alias a media handler passes must reach the same latch the chat path
// recorded, or `ag/...` traffic would keep rotating while antigravity is halted.
func TestHaltedProviderRefusesThroughAnAlias(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "antigravity", 3, "https://mock.example.com")

	h := NewChatHandler(db.NewRepo(database))
	resilience.RecordAccountSuspension("antigravity", "conn-cap-1", http.StatusForbidden, "account suspended")

	if _, _, err := h.GetBestConnection("ag", "", nil, "gemini-3.8-flash"); err == nil {
		t.Error("the alias bypassed the halt")
	}
}

// A probe is the operator's own check that an appeal worked, so it must still
// reach the account it names.
func TestProbeStillReachesAHaltedProvider(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "antigravity", 3, "https://mock.example.com")

	h := NewChatHandler(db.NewRepo(database))
	resilience.RecordAccountSuspension("antigravity", "conn-cap-1", http.StatusForbidden, "account suspended")

	ctx := handlerutil.WithProbeContext(t.Context())
	if _, _, err := h.GetBestConnectionWithContext(ctx, "antigravity", "conn-cap-1", nil, "gemini-3.8-flash"); err != nil {
		t.Errorf("a probe was refused on a halted provider: %v", err)
	}
}

// An unrelated provider must keep serving while one is halted.
func TestResolverHaltIsScopedToOneProvider(t *testing.T) {
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedCappedPool(t, database, "antigravity", 2, "https://mock.example.com")
	seedConnDB(t, database, "deepseek", "conn-ds-keep", "sk-keep", "https://mock.example.com")

	h := NewChatHandler(db.NewRepo(database))
	resilience.RecordAccountSuspension("antigravity", "conn-cap-1", http.StatusForbidden, "account suspended")

	if _, _, err := h.GetBestConnection("deepseek", "", nil, "deepseek-chat"); err != nil {
		t.Errorf("deepseek was refused while antigravity was halted: %v", err)
	}
}

// The cap judges every account in the pool, so its usability check must read
// the row's own JSON rather than issue a SELECT per account: a 40-account pool
// cost 40 statements (80 when the canonical lock key differs) for data already
// in hand, which AGENTS.md §4.A forbids on the §4.G hardware.
//
// Closing the database is the assertion. A helper that queries would get an
// error back and report the account as unlocked; one that reads the blob still
// sees the lock.
func TestAccountCapUsabilityCheckDoesNotTouchTheDatabase(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}

	locked := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	data, _ := json.Marshal(map[string]any{
		"apiKey":                  "sk-1",
		"baseUrl":                 "https://mock.example.com",
		"modelLock_deepseek-chat": locked,
	})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
		VALUES ('conn-nodb', 'deepseek', 'apikey', 'NoDB', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		string(data)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	var conn *models.ProviderConnection
	conns, err := db.NewRepo(database).GetProviderConnections("deepseek", true)
	if err != nil {
		t.Fatalf("load connections: %v", err)
	}
	for _, c := range conns {
		if c.ID == "conn-nodb" {
			conn = c
		}
	}
	if conn == nil {
		t.Fatal("seeded connection was not loaded")
	}

	// Everything the check needs is now in memory.
	database.Close()

	if !connectionUnavailableNow(conn, "deepseek", "deepseek-chat", time.Now()) {
		t.Error("the locked account read as usable once the database was closed: the check is querying instead of reading the row")
	}
}

// The in-memory check must agree with the selection loop's own verdict, or the
// cap would hold back accounts the loop considers fine (or expose ones it does
// not).
func TestAccountCapUsabilityMatchesTheSelectionLoopVerdict(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}

	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)

	cases := []struct {
		id   string
		data map[string]any
	}{
		{"conn-free", map[string]any{"apiKey": "sk-1"}},
		{"conn-model-locked", map[string]any{"apiKey": "sk-1", "modelLock_deepseek-chat": future}},
		{"conn-model-lock-expired", map[string]any{"apiKey": "sk-1", "modelLock_deepseek-chat": past}},
		{"conn-cooled", map[string]any{"apiKey": "sk-1", "rateLimitedUntil": future}},
		{"conn-cooldown-expired", map[string]any{"apiKey": "sk-1", "rateLimitedUntil": past}},
		{"conn-oauth-locked", map[string]any{"apiKey": "sk-1", "oauthLockedUntil": future}},
		{"conn-other-model-locked", map[string]any{"apiKey": "sk-1", "modelLock_other": future}},
	}
	for i, c := range cases {
		blob, _ := json.Marshal(c.data)
		if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
			VALUES (?, 'deepseek', 'apikey', 'Case', ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			c.id, i+1, string(blob)); err != nil {
			t.Fatalf("seed %s: %v", c.id, err)
		}
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	conns, err := repo.GetProviderConnections("deepseek", true)
	if err != nil {
		t.Fatalf("load connections: %v", err)
	}
	if len(conns) != len(cases) {
		t.Fatalf("loaded %d connections, want %d", len(conns), len(cases))
	}

	now := time.Now()
	for _, c := range conns {
		loopBlocked := h.connectionModelBlocked(c, "deepseek", "deepseek-chat")
		if until, ok := db.ConnectionBlockedUntil(c.Data); ok && until.After(now) {
			loopBlocked = true
		}
		if got := connectionUnavailableNow(c, "deepseek", "deepseek-chat", now); got != loopBlocked {
			t.Errorf("%s: in-memory check = %v but the selection loop says %v", c.ID, got, loopBlocked)
		}
	}
}
