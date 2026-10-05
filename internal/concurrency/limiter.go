package concurrency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	// ErrLimitExceeded is returned by AcquireWithTimeout when no slot becomes
	// available before the wait deadline or the caller's context is cancelled.
	ErrLimitExceeded = errors.New("concurrency limit exceeded")
	// ErrNoWait is returned by AcquireWithTimeout when a zero timeout is
	// supplied: the caller asked for a non-blocking acquisition and the limit
	// is already reached.
	ErrNoWait = errors.New("concurrency limit reached without waiting")
)

// DefaultAcquireTimeout is the short grace period callers wait for a concurrency
// slot before rejecting a request with 429.
const DefaultAcquireTimeout = 1500 * time.Millisecond

type keyEntry struct {
	count int
}

// Limiter manages in-memory concurrent active session/request limits per key.
// Map keys are SHA-256 hex digests so plaintext API keys are never retained in
// long-lived process memory.
type Limiter struct {
	mu      sync.RWMutex
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

// hashKey returns the SHA-256 hex digest of key. The plaintext key is never
// stored in the Limiter's map; only this digest is.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// HashKey exposes the SHA-256 hex digest used internally so callers (e.g. the
// dashboard) can match live in-flight counts to API keys without ever passing
// the plaintext key into a long-lived structure.
func HashKey(key string) string {
	return hashKey(key)
}

// Acquire attempts to increment active session count for the given key up to limit.
// If limit <= 0, it is treated as unlimited and returns a no-op release function with ok=true.
// If active count < limit, it increments the count and returns a safe, idempotent release function with ok=true.
// If active count >= limit, it returns ok=false immediately (no waiting).
func (l *Limiter) Acquire(key string, limit int) (func(), bool) {
	if limit <= 0 {
		return func() {}, true
	}

	h := hashKey(key)

	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[h]
	if !exists {
		entry = &keyEntry{count: 0}
		l.entries[h] = entry
	}

	if entry.count >= limit {
		return nil, false
	}

	entry.count++
	return l.makeRelease(h), true
}

// AcquireWithTimeout attempts to acquire a slot like Acquire, but when the
// limit is already reached it waits briefly (grace period) until either a slot
// is released or the timeout/context expires. The returned release function is
// safe and idempotent; the error is non-nil when no slot became available.
func (l *Limiter) AcquireWithTimeout(ctx context.Context, key string, maxConcurrent int, timeout time.Duration) (func(), error) {
	if maxConcurrent <= 0 {
		return func() {}, nil
	}

	h := hashKey(key)

	if timeout <= 0 {
		if l.tryAcquire(h, maxConcurrent) {
			return l.makeRelease(h), nil
		}
		return nil, ErrNoWait
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(4 * time.Millisecond)
	defer ticker.Stop()

	for {
		if l.tryAcquire(h, maxConcurrent) {
			return l.makeRelease(h), nil
		}

		select {
		case <-ctx.Done():
			return nil, ErrLimitExceeded
		case <-timer.C:
			return nil, ErrLimitExceeded
		case <-ticker.C:
			// Slot may have freed up; retry.
		}
	}
}

// tryAcquire is the shared non-blocking acquisition path used by
// AcquireWithTimeout. It hashes are computed by the caller.
func (l *Limiter) tryAcquire(h string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[h]
	if !exists {
		entry = &keyEntry{count: 0}
		l.entries[h] = entry
	}
	if entry.count >= limit {
		return false
	}
	entry.count++
	return true
}

// makeRelease returns an idempotent release func for a previously acquired
// slot. It closes over the hashed key, never the plaintext key.
func (l *Limiter) makeRelease(h string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()

			e, ok := l.entries[h]
			if !ok {
				return
			}
			e.count--
			if e.count <= 0 {
				delete(l.entries, h)
			}
		})
	}
}

// ActiveCount returns current active count for a key (useful for inspection/tests).
// The lookup is performed on the SHA-256 digest, not the plaintext key.
func (l *Limiter) ActiveCount(key string) int {
	h := hashKey(key)

	l.mu.RLock()
	defer l.mu.RUnlock()
	if e, ok := l.entries[h]; ok {
		return e.count
	}
	return 0
}

// GetActiveCounts returns a snapshot of active in-flight counts keyed by the
// SHA-256 hex digest of each API key. Plaintext keys are never exposed or
// retained by the Limiter.
func (l *Limiter) GetActiveCounts() map[string]int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make(map[string]int, len(l.entries))
	for h, e := range l.entries {
		if e.count > 0 {
			out[h] = e.count
		}
	}
	return out
}
