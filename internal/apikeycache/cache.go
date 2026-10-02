// Package apikeycache provides a short-lived, concurrency-safe in-memory cache
// for client API key validation lookups.
//
// It exists in its own leaf package (rather than inside middleware) because both
// middleware (which validates keys) and db (which mutates them) need it, and
// middleware already imports db — a db -> middleware import would be a cycle.
//
// Keys are indexed by SHA-256 fingerprint rather than plaintext, so a memory
// dump of the process never yields usable API keys, and the fingerprint is
// re-checked with crypto/subtle so a lookup cannot be used as a timing oracle.
package apikeycache

import (
	"crypto/sha256"
	"crypto/subtle"
	"sync"
	"time"

	"9router/proxy/internal/models"
)

// DefaultTTL bounds how long a cached key stays valid. Long enough to absorb
// bursts of authenticated traffic without holding SQLite locks, short enough
// that a deactivated key becomes useless within a minute even if every
// invalidation path were missed.
const DefaultTTL = 60 * time.Second

// fingerprint is a SHA-256 digest of the presented key. Using a fixed-size
// digest as the map key keeps the hot path allocation-free of plaintext and
// makes the constant-time comparison meaningful (subtle.ConstantTimeCompare
// short-circuits on length mismatch, so both sides must always be 32 bytes).
type fingerprint [sha256.Size]byte

// cachedKey is one cached validation result.
type cachedKey struct {
	fingerprint fingerprint
	keyObj      *models.APIKey
	expiresAt   time.Time
}

var (
	mu      sync.RWMutex
	entries = make(map[fingerprint]cachedKey)

	// ttl is a variable rather than a const so tests can exercise expiry
	// without sleeping for a minute.
	ttl = DefaultTTL

	// now is injectable for deterministic expiry tests.
	now = time.Now
)

// Fingerprint hashes an API key into the digest used as the cache index.
func Fingerprint(key string) fingerprint {
	return sha256.Sum256([]byte(key))
}

// Lookup returns the cached key object for key when a fresh entry exists.
// The second result is false when the key was never cached or its entry has
// expired, in which case the caller must consult the database.
func Lookup(key string) (*models.APIKey, bool) {
	fp := Fingerprint(key)
	nowFn := now

	mu.RLock()
	entry, ok := entries[fp]
	mu.RUnlock()

	if !ok {
		return nil, false
	}

	// Constant-time confirmation that the entry really belongs to this key,
	// so Lookup does not leak how much of a guessed key was correct.
	if subtle.ConstantTimeCompare(entry.fingerprint[:], fp[:]) != 1 {
		return nil, false
	}

	if nowFn().After(entry.expiresAt) {
		mu.Lock()
		// Re-check under the write lock so a concurrent Store of a fresher
		// entry is not dropped by this cleanup.
		if current, still := entries[fp]; still && current.expiresAt.Equal(entry.expiresAt) {
			delete(entries, fp)
		}
		mu.Unlock()
		return nil, false
	}

	return entry.keyObj, true
}

// Store caches keyObj for key until the current TTL elapses.
func Store(key string, keyObj *models.APIKey) {
	if keyObj == nil {
		return
	}

	fp := Fingerprint(key)
	nowFn := now
	ttlDur := ttl

	mu.Lock()
	entries[fp] = cachedKey{fingerprint: fp, keyObj: keyObj, expiresAt: nowFn().Add(ttlDur)}
	mu.Unlock()
}

// Invalidate drops every cached entry. Call it whenever apiKeys rows change so
// a revoked key cannot keep authenticating from a warm cache.
func Invalidate() {
	mu.Lock()
	clear(entries)
	mu.Unlock()
}

// Len reports the number of live (not yet expired) cached entries.
func Len() int {
	nowFn := now
	mu.RLock()
	defer mu.RUnlock()

	live := 0
	for _, entry := range entries {
		if !nowFn().After(entry.expiresAt) {
			live++
		}
	}
	return live
}

// SetTTL overrides the cache TTL and returns a function that restores the
// previous value, so callers (tests) can scope the override to one test.
func SetTTL(d time.Duration) func() {
	mu.Lock()
	previous := ttl
	ttl = d
	mu.Unlock()

	return func() {
		mu.Lock()
		ttl = previous
		mu.Unlock()
	}
}

// SetClock overrides the time source and returns a function that restores it.
func SetClock(fn func() time.Time) func() {
	mu.Lock()
	previous := now
	now = fn
	mu.Unlock()

	return func() {
		mu.Lock()
		now = previous
		mu.Unlock()
	}
}
