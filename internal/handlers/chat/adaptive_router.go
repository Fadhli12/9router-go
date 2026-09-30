package chat

import (
	"math"
	"sync"
	"time"

	"9router/proxy/internal/models"
)

// CircuitState represents the health status of an upstream connection
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"    // Normal, healthy
	CircuitHalfOpen CircuitState = "half_open" // Testing recovery
	CircuitOpen     CircuitState = "open"      // Tripped / quarantined
)

type ConnectionHealthStats struct {
	Circuit      CircuitState
	FailureCount int
	SuccessCount int
	LastFailure  time.Time
	LastSuccess  time.Time
	LatencyEMA   float64 // Exponentially Weighted Moving Average in ms
}

type AdaptiveRouter struct {
	mu    sync.RWMutex
	stats map[string]*ConnectionHealthStats
}

var GlobalAdaptiveRouter = &AdaptiveRouter{
	stats: make(map[string]*ConnectionHealthStats),
}

// GetOrCreateStats returns pointer to stats (must be called with lock held or via methods)
func (r *AdaptiveRouter) getOrCreate(connID string) *ConnectionHealthStats {
	s, ok := r.stats[connID]
	if !ok {
		s = &ConnectionHealthStats{
			Circuit:    CircuitClosed,
			LatencyEMA: 250.0, // Initial default baseline 250ms
		}
		r.stats[connID] = s
	}
	return s
}

// RecordSuccess updates latency EMA and restores circuit state if half-open
func (r *AdaptiveRouter) RecordSuccess(connID string, latency time.Duration) {
	if connID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.getOrCreate(connID)
	s.SuccessCount++
	s.LastSuccess = time.Now()

	// EWMA alpha = 0.2
	latencyMs := float64(latency.Milliseconds())
	if latencyMs <= 0 {
		latencyMs = 1.0
	}
	s.LatencyEMA = 0.2*latencyMs + 0.8*s.LatencyEMA

	if s.Circuit == CircuitHalfOpen {
		s.Circuit = CircuitClosed
		s.FailureCount = 0
	}
}

// RecordFailure trips circuit breaker on consecutive errors or fatal errors
func (r *AdaptiveRouter) RecordFailure(connID string, isFatal bool) {
	if connID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	s := r.getOrCreate(connID)
	s.FailureCount++
	s.LastFailure = time.Now()

	if isFatal || s.FailureCount >= 3 {
		s.Circuit = CircuitOpen
	}
}

// ScoreConnection computes the OmniRoute-inspired multi-factor routing score:
// Score = Health * Reliability * LatencyFactor * CircuitFactor
func (r *AdaptiveRouter) ScoreConnection(conn *models.ProviderConnection) float64 {
	if conn == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.stats[conn.ID]
	if !ok {
		return 1.0 // Fresh un-benchmarked connection
	}

	// 1. Circuit breaker factor
	circuitFactor := 1.0
	if s.Circuit == CircuitOpen {
		// Auto half-open recovery after 3 minutes
		if time.Since(s.LastFailure) > 3*time.Minute {
			circuitFactor = 0.5
		} else {
			return 0.0 // Quarantined
		}
	} else if s.Circuit == CircuitHalfOpen {
		circuitFactor = 0.5
	}

	// 2. Latency factor (penalize EMA > 1500ms)
	latencyFactor := 1.0
	if s.LatencyEMA > 0 {
		latencyFactor = math.Max(0.2, 1.0-math.Min(s.LatencyEMA, 5000.0)/6000.0)
	}

	// 3. Reliability factor
	totalAttempts := s.SuccessCount + s.FailureCount
	reliability := 1.0
	if totalAttempts > 0 {
		reliability = math.Max(0.1, float64(s.SuccessCount)/float64(totalAttempts))
	}

	return circuitFactor * latencyFactor * reliability
}
