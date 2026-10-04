package concurrency

import (
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
	l.mu.Lock()
	_, exists := l.entries["keyA"]
	l.mu.Unlock()
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
