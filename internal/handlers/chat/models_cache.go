package chat

import (
	"context"
	"sync"
	"time"
)

// modelsCacheTTL bounds how long a derived /v1/models catalog stays
// authoritative. The catalog is rebuilt from every provider connection row plus
// the static registry, so a burst of client discovery calls (a CLI refreshing
// its model picker, several IDEs booting at once) otherwise re-walks the same
// rows and re-serializes the same ~1300-entry catalog per request.
const modelsCacheTTL = 30 * time.Second

// modelsCacheEntry is one memoized catalog plus the instant it stops being
// authoritative.
type modelsCacheEntry struct {
	result    ModelsListResult
	expiresAt time.Time
}

// modelsCache is a TTL memo of buildModelsListResult, keyed by ModelsListMode
// because the three modes publish genuinely different catalogs (a full static
// dump, a connected-only set, an all-rows set) that must never alias.
//
// The zero value is ready to use: the map is created lazily under the write
// lock, so a ChatHandler built as a struct literal caches without a constructor
// change.
type modelsCache struct {
	mu      sync.RWMutex
	entries map[ModelsListMode]modelsCacheEntry
}

// load returns the cached catalog for mode when it is present and unexpired.
//
// The stored ModelsListResult is returned by value, sharing its Models slice.
// That is deliberate: the slice is never mutated after it is stored — callers
// only serialize it — so concurrent readers need no copy, and deep-copying
// ~1300 entries per request would defeat the cache.
func (c *modelsCache) load(mode ModelsListMode) (ModelsListResult, bool) {
	c.mu.RLock()
	entry, ok := c.entries[mode]
	c.mu.RUnlock()

	if !ok || !time.Now().Before(entry.expiresAt) {
		return ModelsListResult{}, false
	}
	return entry.result, true
}

// store memoizes result for mode with a fresh expiry.
func (c *modelsCache) store(mode ModelsListMode, result ModelsListResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[ModelsListMode]modelsCacheEntry, 3)
	}
	c.entries[mode] = modelsCacheEntry{
		result:    result,
		expiresAt: time.Now().Add(modelsCacheTTL),
	}
}

// invalidate drops every mode, so the next read re-derives from the database.
func (c *modelsCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = nil
}

// InvalidateModelsCache clears the memoized /v1/models catalog. Call it after a
// write that changes which models the gateway should publish (connection added,
// removed, enabled, or disabled), so the next listing reflects the change
// instead of serving up to modelsCacheTTL of stale catalog.
func (h *ChatHandler) InvalidateModelsCache() {
	h.modelsCache.invalidate()
}

// modelsListResultCached is the read-through wrapper around
// buildModelsListResult: serve the memoized catalog when one is live, otherwise
// derive, memoize, and return.
func (h *ChatHandler) modelsListResultCached(ctx context.Context, mode ModelsListMode) ModelsListResult {
	if cached, ok := h.modelsCache.load(mode); ok {
		return cached
	}

	result := h.buildModelsListResult(ctx, mode)
	h.modelsCache.store(mode, result)
	return result
}
