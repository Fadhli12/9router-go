package chat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"9router/proxy/internal/db"
)

func TestModelsCacheTTL(t *testing.T) {
	if modelsCacheTTL != 30*time.Second {
		t.Fatalf("modelsCacheTTL = %v, want 30s", modelsCacheTTL)
	}
}

func TestModelsCacheLoad(t *testing.T) {
	stored := ModelsListResult{
		Models:      []ModelInfoObject{{ID: "ds/deepseek-chat", Object: "model", OwnedBy: "ds"}},
		Mode:        "all",
		Connections: 2,
	}

	tests := []struct {
		name      string
		stored    map[ModelsListMode]modelsCacheEntry
		mode      ModelsListMode
		wantHit   bool
		wantConns int
	}{
		{
			name:      "live entry is a hit",
			stored:    map[ModelsListMode]modelsCacheEntry{modeListAll: {result: stored, expiresAt: time.Now().Add(time.Minute)}},
			mode:      modeListAll,
			wantHit:   true,
			wantConns: 2,
		},
		{
			name:    "absent mode is a miss",
			stored:  map[ModelsListMode]modelsCacheEntry{modeListAll: {result: stored, expiresAt: time.Now().Add(time.Minute)}},
			mode:    modeListCatalog,
			wantHit: false,
		},
		{
			name:    "past expiry is a miss",
			stored:  map[ModelsListMode]modelsCacheEntry{modeListAll: {result: stored, expiresAt: time.Now().Add(-time.Second)}},
			mode:    modeListAll,
			wantHit: false,
		},
		{
			name:    "zero expiry is a miss",
			stored:  map[ModelsListMode]modelsCacheEntry{modeListAll: {result: stored}},
			mode:    modeListAll,
			wantHit: false,
		},
		{
			name:    "empty map is a miss",
			stored:  map[ModelsListMode]modelsCacheEntry{},
			mode:    modeListAll,
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c modelsCache
			c.entries = tt.stored

			got, hit := c.load(tt.mode)
			if hit != tt.wantHit {
				t.Fatalf("load() hit = %v, want %v", hit, tt.wantHit)
			}
			if !tt.wantHit {
				return
			}
			if got.Connections != tt.wantConns {
				t.Errorf("load() Connections = %d, want %d", got.Connections, tt.wantConns)
			}
			if len(got.Models) != 1 || got.Models[0].ID != "ds/deepseek-chat" {
				t.Errorf("load() Models = %+v, want the stored entry", got.Models)
			}
		})
	}
}

func TestModelsCacheStoreOnZeroValue(t *testing.T) {
	var c modelsCache

	if _, hit := c.load(modeListAll); hit {
		t.Fatal("zero-value cache must miss")
	}

	c.store(modeListAll, ModelsListResult{Models: []ModelInfoObject{{ID: "ds/deepseek-chat"}}, Connections: 1})

	got, hit := c.load(modeListAll)
	if !hit {
		t.Fatal("store on zero value must make the entry readable")
	}
	if got.Connections != 1 {
		t.Errorf("Connections = %d, want 1", got.Connections)
	}
}

func TestModelsCacheInvalidate(t *testing.T) {
	tests := []struct {
		name   string
		prime  func(c *modelsCache)
		after  []ModelsListMode
		absent bool
	}{
		{
			name:  "clears every mode",
			prime: func(c *modelsCache) { c.store(modeListAll, ModelsListResult{}); c.store(modeListConnected, ModelsListResult{}); c.store(modeListCatalog, ModelsListResult{}) },
			after: []ModelsListMode{modeListAll, modeListConnected, modeListCatalog},
		},
		{
			name:  "clears a single mode",
			prime: func(c *modelsCache) { c.store(modeListAll, ModelsListResult{}); c.store(modeListConnected, ModelsListResult{}) },
			after: []ModelsListMode{modeListAll},
		},
		{
			name:  "no-op on empty cache",
			prime: func(c *modelsCache) {},
			after: []ModelsListMode{modeListAll, modeListConnected},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c modelsCache
			tt.prime(&c)

			c.invalidate()

			for _, mode := range tt.after {
				if _, hit := c.load(mode); hit {
					t.Errorf("mode %v still cached after invalidate", mode)
				}
			}
		})
	}
}

func TestModelsCacheModesDoNotAlias(t *testing.T) {
	var c modelsCache
	c.store(modeListAll, ModelsListResult{Models: []ModelInfoObject{{ID: "all/model"}}, Mode: "all"})
	c.store(modeListConnected, ModelsListResult{Models: []ModelInfoObject{{ID: "connected/model"}}, Mode: "connected"})

	all, _ := c.load(modeListAll)
	connected, _ := c.load(modeListConnected)
	if all.Models[0].ID != "all/model" {
		t.Errorf("modeListAll returned %q, want all/model", all.Models[0].ID)
	}
	if connected.Models[0].ID != "connected/model" {
		t.Errorf("modeListConnected returned %q, want connected/model", connected.Models[0].ID)
	}
}

// TestHandleModelsServesCachedCatalog proves the read-through path: a second
// listing inside the TTL window answers from memory, so a connection row added
// in between is not yet visible.
func TestHandleModelsServesCachedCatalog(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	h := NewChatHandler(db.NewRepo(database))

	before := strings.Join(modelsIDs(t, h), "\n")
	if before == "" {
		t.Fatal("expected a non-empty catalog before invalidation")
	}

	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-late', 'qoder', 'apikey', 'Late', 1, 1, '{"apiKey":"tok-late"}', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("seed late connection: %v", err)
	}

	if cached := strings.Join(modelsIDs(t, h), "\n"); cached != before {
		t.Error("second listing inside the TTL must be served from cache")
	}

	h.InvalidateModelsCache()

	after := strings.Join(modelsIDs(t, h), "\n")
	if after == before {
		t.Error("InvalidateModelsCache must make the next listing re-derive from the database")
	}
}

func TestHandleModelsRederivesAfterExpiry(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	h := NewChatHandler(db.NewRepo(database))
	expected := strings.Join(modelsIDs(t, h), "\n")

	// Poison the entry with an already-expired sentinel: HandleModels must treat
	// it as a miss and rebuild rather than publish the stale payload.
	h.modelsCache.mu.Lock()
	h.modelsCache.entries = map[ModelsListMode]modelsCacheEntry{
		modeListAll: {
			result:    ModelsListResult{Models: []ModelInfoObject{{ID: "sentinel/model"}}, Mode: "all"},
			expiresAt: time.Now().Add(-time.Millisecond),
		},
	}
	h.modelsCache.mu.Unlock()

	if got := strings.Join(modelsIDs(t, h), "\n"); got != expected {
		t.Error("an expired entry must be re-derived, not served")
	}
	if _, hit := h.modelsCache.load(modeListAll); !hit {
		t.Error("the re-derived catalog must be re-cached")
	}
}

func TestModelsCacheConcurrentAccess(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	h := NewChatHandler(db.NewRepo(database))
	modes := []ModelsListMode{modeListAll, modeListConnected, modeListCatalog}

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				mode := modes[(worker+n)%len(modes)]
				h.modelsListResultCached(context.Background(), mode)
				h.modelsCache.load(mode)
				if n%7 == 0 {
					h.InvalidateModelsCache()
				}
			}
		}(i)
	}
	wg.Wait()
}
