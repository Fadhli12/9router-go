package apikeycache

import (
	"crypto/sha256"
	"testing"
	"time"

	"9router/proxy/internal/models"
)

// resetCache returns the package globals to their defaults so a test cannot
// leak a short TTL or a fake clock into the next one.
func resetCache(t *testing.T) {
	t.Helper()
	Invalidate()
	t.Cleanup(func() {
		Invalidate()
		SetTTL(DefaultTTL)()
		SetClock(time.Now)()
	})
}

// apiKey builds a minimal active key object for caching tests.
func apiKey(id string) *models.APIKey {
	return &models.APIKey{
		ID:        id,
		Key:       "sk-" + id,
		IsActive:  1,
		CreatedAt: "2026-01-01T00:00:00Z",
	}
}

func TestStoreThenLookup(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantID  string
		wantHit bool
	}{
		{name: "stored key is returned", key: "sk-live-abc", wantID: "key-1", wantHit: true},
		{name: "different key misses", key: "sk-live-xyz", wantHit: false},
		{name: "empty key misses", key: "", wantHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCache(t)
			Store("sk-live-abc", apiKey("key-1"))

			got, ok := Lookup(tt.key)
			if ok != tt.wantHit {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.key, ok, tt.wantHit)
			}
			if tt.wantHit && got.ID != tt.wantID {
				t.Errorf("Lookup(%q).ID = %q, want %q", tt.key, got.ID, tt.wantID)
			}
		})
	}
}

func TestStoreNilKeyIsIgnored(t *testing.T) {
	resetCache(t)

	Store("sk-live-abc", nil)

	if _, ok := Lookup("sk-live-abc"); ok {
		t.Fatal("Lookup() hit after Store(key, nil); nil key objects must not be cached")
	}
	if n := Len(); n != 0 {
		t.Errorf("Len() = %d after Store(key, nil), want 0", n)
	}
}

func TestStoreOverwritesExistingEntry(t *testing.T) {
	resetCache(t)

	Store("sk-live-abc", apiKey("old"))
	Store("sk-live-abc", apiKey("new"))

	got, ok := Lookup("sk-live-abc")
	if !ok {
		t.Fatal("Lookup() miss after re-Store, want hit")
	}
	if got.ID != "new" {
		t.Errorf("Lookup().ID = %q, want %q (re-Store must replace the entry)", got.ID, "new")
	}
	if n := Len(); n != 1 {
		t.Errorf("Len() = %d after re-Store of same key, want 1", n)
	}
}

func TestLookupExpiresAfterTTL(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	restoreClock := SetClock(func() time.Time { return clock })
	defer restoreClock()
	restoreTTL := SetTTL(time.Minute)
	defer restoreTTL()

	Invalidate()
	defer Invalidate()

	Store("sk-live-abc", apiKey("key-1"))

	if _, ok := Lookup("sk-live-abc"); !ok {
		t.Fatal("Lookup() miss immediately after Store, want hit")
	}

	tests := []struct {
		name    string
		advance time.Duration
		wantHit bool
	}{
		// An entry is live until expiresAt inclusive; the check is After(), not
		// !Before(), so the exact expiry instant still serves the cached key.
		{name: "just inside TTL", advance: 59 * time.Second, wantHit: true},
		{name: "exactly at TTL is still live", advance: 60 * time.Second, wantHit: true},
		{name: "one nanosecond past TTL", advance: 60*time.Second + 1, wantHit: false},
		{name: "well past TTL", advance: 5 * time.Minute, wantHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock = clock.Add(tt.advance)
			defer func() { clock = clock.Add(-tt.advance) }()

			_, ok := Lookup("sk-live-abc")
			if ok != tt.wantHit {
				t.Fatalf("Lookup() ok = %v after advancing %s, want %v", ok, tt.advance, tt.wantHit)
			}
		})
	}
}

func TestExpiredLookupEvictsEntry(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	restoreClock := SetClock(func() time.Time { return clock })
	defer restoreClock()
	restoreTTL := SetTTL(time.Minute)
	defer restoreTTL()

	Invalidate()
	defer Invalidate()

	Store("sk-live-abc", apiKey("key-1"))

	// Store the second key 30s later so it outlives the first one, proving the
	// eviction of the stale entry is targeted and not a blanket flush.
	clock = clock.Add(30 * time.Second)
	Store("sk-live-xyz", apiKey("key-2"))

	// Now both are past the 1m TTL only for the first.
	clock = clock.Add(31 * time.Second)
	if _, ok := Lookup("sk-live-abc"); ok {
		t.Fatal("Lookup() hit for expired key, want miss")
	}

	// Len() counts live entries only, so the stale one must not inflate it.
	if n := Len(); n != 1 {
		t.Errorf("Len() = %d, want 1 (expired entry should be dropped)", n)
	}
	if _, ok := Lookup("sk-live-xyz"); !ok {
		t.Error("Lookup() miss for unexpired key; evicting one entry dropped another")
	}
}

func TestFingerprintIsDeterministicAndFixedSize(t *testing.T) {
	const key = "sk-live-abc"

	first := Fingerprint(key)
	second := Fingerprint(key)

	if first != second {
		t.Error("Fingerprint() is not deterministic for the same key")
	}
	if len(first) != sha256.Size {
		t.Errorf("len(Fingerprint()) = %d, want %d", len(first), sha256.Size)
	}
}

func TestFingerprintDistinguishesKeys(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		other string
	}{
		{name: "completely different", key: "sk-live-abc", other: "sk-live-xyz"},
		{name: "one byte apart", key: "sk-live-abc", other: "sk-live-abd"},
		{name: "prefix of the other", key: "sk-live-ab", other: "sk-live-abc"},
		{name: "empty vs non-empty", key: "", other: "sk-live-abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if Fingerprint(tt.key) == Fingerprint(tt.other) {
				t.Fatalf("Fingerprint(%q) == Fingerprint(%q); distinct keys must not collide", tt.key, tt.other)
			}
		})
	}
}

func TestLookupDoesNotMatchNeighbouringKey(t *testing.T) {
	resetCache(t)

	Store("sk-live-abc", apiKey("key-1"))

	// A near-miss must miss, otherwise Lookup leaks how much of a guess was
	// right and the constant-time confirmation is pointless.
	for _, guess := range []string{"sk-live-ab", "sk-live-abd", "sk-live-abcx", "SK-LIVE-ABC"} {
		if got, ok := Lookup(guess); ok {
			t.Errorf("Lookup(%q) hit with %+v, want miss", guess, got)
		}
	}
}

func TestLookupChecksFingerprintBeforeReturning(t *testing.T) {
	resetCache(t)

	// Plant an entry under one fingerprint whose stored fingerprint field
	// disagrees, mimicking a tampered or corrupted map value.
	planted := Fingerprint("sk-planted")
	mu.Lock()
	entries[planted] = cachedKey{
		fingerprint: Fingerprint("sk-different"),
		keyObj:      apiKey("key-1"),
		expiresAt:   time.Now().Add(DefaultTTL),
	}
	mu.Unlock()

	if _, ok := Lookup("sk-planted"); ok {
		t.Error("Lookup() hit on a fingerprint mismatch; the constant-time compare must reject it")
	}
}

func TestInvalidateDropsEverything(t *testing.T) {
	resetCache(t)

	Store("sk-live-abc", apiKey("key-1"))
	Store("sk-live-xyz", apiKey("key-2"))
	Store("sk-live-def", apiKey("key-3"))

	if n := Len(); n != 3 {
		t.Fatalf("Len() = %d after 3 Store calls, want 3", n)
	}

	Invalidate()

	if n := Len(); n != 0 {
		t.Errorf("Len() = %d after Invalidate, want 0", n)
	}
	for _, key := range []string{"sk-live-abc", "sk-live-xyz", "sk-live-def"} {
		if _, ok := Lookup(key); ok {
			t.Errorf("Lookup(%q) hit after Invalidate, want miss", key)
		}
	}
}

func TestInvalidateOnEmptyCacheIsNoop(t *testing.T) {
	resetCache(t)

	Invalidate()
	Invalidate()

	if n := Len(); n != 0 {
		t.Errorf("Len() = %d after repeated Invalidate, want 0", n)
	}
}

func TestInvalidateKeepsCacheUsable(t *testing.T) {
	resetCache(t)

	Store("sk-live-abc", apiKey("key-1"))
	Invalidate()
	Store("sk-live-abc", apiKey("key-2"))

	got, ok := Lookup("sk-live-abc")
	if !ok {
		t.Fatal("Lookup() miss after Store following Invalidate, want hit")
	}
	if got.ID != "key-2" {
		t.Errorf("Lookup().ID = %q, want %q", got.ID, "key-2")
	}
}

func TestLen(t *testing.T) {
	tests := []struct {
		name  string
		store []string
		want  int
	}{
		{name: "empty cache", store: nil, want: 0},
		{name: "single entry", store: []string{"sk-a"}, want: 1},
		{name: "three distinct entries", store: []string{"sk-a", "sk-b", "sk-c"}, want: 3},
		{name: "duplicate stores collapse", store: []string{"sk-a", "sk-a", "sk-a"}, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCache(t)
			for i, key := range tt.store {
				Store(key, apiKey(string(rune('a' + i))))
			}

			if got := Len(); got != tt.want {
				t.Errorf("Len() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLenExcludesExpiredEntries(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	restoreClock := SetClock(func() time.Time { return clock })
	defer restoreClock()
	restoreTTL := SetTTL(time.Minute)
	defer restoreTTL()

	Invalidate()
	defer Invalidate()

	Store("sk-a", apiKey("key-1"))
	Store("sk-b", apiKey("key-2"))

	if got := Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}

	clock = clock.Add(2 * time.Minute)

	if got := Len(); got != 0 {
		t.Errorf("Len() = %d after TTL elapsed, want 0", got)
	}
}

func TestStoreHonoursShortTTL(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	restoreClock := SetClock(func() time.Time { return clock })
	defer restoreClock()
	restoreTTL := SetTTL(10 * time.Second)
	defer restoreTTL()

	Invalidate()
	defer Invalidate()

	Store("sk-a", apiKey("key-1"))
	if _, ok := Lookup("sk-a"); !ok {
		t.Fatal("Lookup() miss right after Store, want hit")
	}

	clock = clock.Add(11 * time.Second)

	if _, ok := Lookup("sk-a"); ok {
		t.Error("Lookup() hit after the 10s TTL elapsed, want miss")
	}
}

func TestSetTTLRestoresPreviousValue(t *testing.T) {
	resetCache(t)

	restore := SetTTL(5 * time.Second)
	mu.RLock()
	got := ttl
	mu.RUnlock()
	if got != 5*time.Second {
		t.Fatalf("ttl = %s after SetTTL, want 5s", got)
	}

	restore()
	mu.RLock()
	got = ttl
	mu.RUnlock()
	if got != DefaultTTL {
		t.Errorf("ttl = %s after restore, want DefaultTTL %s", got, DefaultTTL)
	}
}

func TestConcurrentStoreLookupAndInvalidate(t *testing.T) {
	resetCache(t)

	const workers = 8
	done := make(chan struct{})

	for w := 0; w < workers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			key := "sk-concurrent-" + string(rune('a'+w))
			for i := 0; i < 500; i++ {
				Store(key, apiKey("key-1"))
				Lookup(key)
				Len()
				if i%100 == 0 {
					Invalidate()
				}
			}
		}(w)
	}

	for w := 0; w < workers; w++ {
		<-done
	}

	// The contract is only that the cache stays consistent; whether a given key
	// survived an Invalidate is intentionally unspecified.
	Invalidate()
	if n := Len(); n != 0 {
		t.Errorf("Len() = %d after final Invalidate, want 0", n)
	}
}