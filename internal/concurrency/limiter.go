package concurrency

import (
	"sync"
)

type keyEntry struct {
	count int
}

// Limiter manages in-memory concurrent active session/request limits per key.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*keyEntry
}

// NewLimiter creates a new thread-safe concurrency Limiter.
func NewLimiter() *Limiter {
	return &Limiter{
		entries: make(map[string]*keyEntry),
	}
}

// GlobalLimiter is the shared singleton Limiter for API key concurrency control.
var GlobalLimiter = NewLimiter()

// Acquire attempts to increment active session count for the given key up to limit.
// If limit <= 0, it is treated as unlimited and returns a no-op release function with ok=true.
// If active count < limit, it increments the count and returns a safe, idempotent release function with ok=true.
// If active count >= limit, it returns ok=false.
func (l *Limiter) Acquire(key string, limit int) (func(), bool) {
	if limit <= 0 {
		return func() {}, true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[key]
	if !exists {
		entry = &keyEntry{count: 0}
		l.entries[key] = entry
	}

	if entry.count >= limit {
		return nil, false
	}

	entry.count++

	var once sync.Once
	release := func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()

			e, ok := l.entries[key]
			if !ok {
				return
			}
			e.count--
			if e.count <= 0 {
				delete(l.entries, key)
			}
		})
	}

	return release, true
}

// ActiveCount returns current active count for a key (useful for inspection/tests).
func (l *Limiter) ActiveCount(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[key]; ok {
		return e.count
	}
	return 0
}
