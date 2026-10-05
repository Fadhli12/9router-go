package concurrency

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLimiter_Unlimited(t *testing.T) {
	l := NewLimiter()
	release, ok := l.Acquire("key1", 0)
	if !ok {
		t.Fatal("expected ok=true for limit=0")
	}
	if release == nil {
		t.Fatal("expected non-nil release func")
	}
	release()
	if l.ActiveCount("key1") != 0 {
		t.Fatalf("expected count 0, got %d", l.ActiveCount("key1"))
	}

	releaseNeg, okNeg := l.Acquire("key1", -1)
	if !okNeg {
		t.Fatal("expected ok=true for negative limit")
	}
	releaseNeg()
}

func TestLimiter_EnforcesLimit(t *testing.T) {
	l := NewLimiter()
	const limit = 2

	r1, ok1 := l.Acquire("keyA", limit)
	if !ok1 || r1 == nil {
		t.Fatalf("expected acquire 1 to succeed")
	}
	if l.ActiveCount("keyA") != 1 {
		t.Fatalf("expected active count 1, got %d", l.ActiveCount("keyA"))
	}

	r2, ok2 := l.Acquire("keyA", limit)
	if !ok2 || r2 == nil {
		t.Fatalf("expected acquire 2 to succeed")
	}
	if l.ActiveCount("keyA") != 2 {
		t.Fatalf("expected active count 2, got %d", l.ActiveCount("keyA"))
	}

	r3, ok3 := l.Acquire("keyA", limit)
	if ok3 || r3 != nil {
		t.Fatalf("expected acquire 3 to fail when limit is 2")
	}

	// Release one slot
	r1()
	if l.ActiveCount("keyA") != 1 {
		t.Fatalf("expected active count 1 after r1, got %d", l.ActiveCount("keyA"))
	}

	// Idempotent release check (calling r1 again should do nothing)
	r1()
	if l.ActiveCount("keyA") != 1 {
		t.Fatalf("expected active count 1 after duplicate r1, got %d", l.ActiveCount("keyA"))
	}

	// Now acquire 3 should succeed
	r3, ok3 = l.Acquire("keyA", limit)
	if !ok3 || r3 == nil {
		t.Fatalf("expected acquire 3 to succeed after slot released")
	}
	if l.ActiveCount("keyA") != 2 {
		t.Fatalf("expected active count 2, got %d", l.ActiveCount("keyA"))
	}

	r2()
	r3()
	if l.ActiveCount("keyA") != 0 {
		t.Fatalf("expected active count 0, got %d", l.ActiveCount("keyA"))
	}

	// Entry must be deleted from map when 0
	l.mu.RLock()
	_, exists := l.entries[hashKey("keyA")]
	l.mu.RUnlock()
	if exists {
		t.Fatalf("expected keyA entry to be evicted from map when count is 0")
	}
}

func TestLimiter_ConcurrentGoroutines(t *testing.T) {
	l := NewLimiter()
	const limit = 5
	const numGoroutines = 50

	var wg sync.WaitGroup
	var acquiredCount int
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, ok := l.Acquire("keyConc", limit)
			if ok {
				mu.Lock()
				acquiredCount++
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				rel()
			}
		}()
	}

	wg.Wait()

	if l.ActiveCount("keyConc") != 0 {
		t.Fatalf("expected final active count 0, got %d", l.ActiveCount("keyConc"))
	}
}

func TestLimiter_AcquireWithTimeout_WaitsForSlot(t *testing.T) {
	l := NewLimiter()
	const limit = 1

	r1, ok := l.Acquire("waitKey", limit)
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}

	releaseAfter := 40 * time.Millisecond
	go func() {
		time.Sleep(releaseAfter)
		r1()
	}()

	start := time.Now()
	r2, err := l.AcquireWithTimeout(context.Background(), "waitKey", limit, 2*time.Second)
	if err != nil {
		t.Fatalf("expected AcquireWithTimeout to succeed after slot release, got %v", err)
	}
	if r2 == nil {
		t.Fatal("expected non-nil release func")
	}
	if elapsed := time.Since(start); elapsed < releaseAfter {
		t.Fatalf("expected to wait for slot release (>= %v), got %v", releaseAfter, elapsed)
	}
	r2()
	if l.ActiveCount("waitKey") != 0 {
		t.Fatalf("expected active count 0, got %d", l.ActiveCount("waitKey"))
	}
}

func TestLimiter_AcquireWithTimeout_TimesOut(t *testing.T) {
	l := NewLimiter()
	const limit = 1

	r1, ok := l.Acquire("timeoutKey", limit)
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}
	defer r1()

	start := time.Now()
	r2, err := l.AcquireWithTimeout(context.Background(), "timeoutKey", limit, 50*time.Millisecond)
	if err == nil {
		r2()
		t.Fatal("expected AcquireWithTimeout to time out")
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("expected to wait until timeout, got %v", elapsed)
	}
	if l.ActiveCount("timeoutKey") != 1 {
		t.Fatalf("expected active count to remain 1, got %d", l.ActiveCount("timeoutKey"))
	}
}

func TestLimiter_AcquireWithTimeout_ContextCancel(t *testing.T) {
	l := NewLimiter()
	const limit = 1

	r1, ok := l.Acquire("ctxKey", limit)
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}
	defer r1()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := l.AcquireWithTimeout(ctx, "ctxKey", limit, 2*time.Second)
	if err == nil {
		t.Fatal("expected AcquireWithTimeout to fail on context cancellation")
	}
}

func TestLimiter_AcquireWithTimeout_NoWait(t *testing.T) {
	l := NewLimiter()
	const limit = 1

	r1, ok := l.Acquire("noWaitKey", limit)
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}
	defer r1()

	_, err := l.AcquireWithTimeout(context.Background(), "noWaitKey", limit, 0)
	if err != ErrNoWait {
		t.Fatalf("expected ErrNoWait, got %v", err)
	}
}

func TestLimiter_GetActiveCounts(t *testing.T) {
	l := NewLimiter()

	r1, ok := l.Acquire("countsKey", 3)
	if !ok {
		t.Fatal("expected acquire to succeed")
	}
	defer r1()

	counts := l.GetActiveCounts()
	keyHash := hashKey("countsKey")
	if counts[keyHash] != 1 {
		t.Fatalf("expected active count 1 for %s, got %v", keyHash, counts)
	}

	// The snapshot must not expose plaintext keys.
	if _, found := counts["countsKey"]; found {
		t.Fatal("GetActiveCounts must only contain hashed keys")
	}
}
