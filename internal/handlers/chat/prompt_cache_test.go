package chat

import (
	"testing"
	"time"

	"9router/proxy/internal/models"
)

func TestPromptPrefixAnalysis(t *testing.T) {
	messages := []map[string]any{
		{"role": "system", "content": "You are a helpful coding assistant."},
		{"role": "assistant", "content": "Hello! How can I help you today?"},
		{"role": "user", "content": "Write a quicksort in Go."},
	}

	analysis := AnalyzePromptPrefix(messages)
	if analysis.PrefixHash == "" {
		t.Fatalf("expected non-empty prefix hash")
	}
	if analysis.PrefixEndIdx != 1 {
		t.Fatalf("expected prefix to stop after system/assistant prefix, got %d", analysis.PrefixEndIdx)
	}

	// Affinity store test
	SetPromptCacheAffinity(analysis.PrefixHash, "conn-account-1")
	connID, ok := GetPromptCacheAffinity(analysis.PrefixHash)
	if !ok || connID != "conn-account-1" {
		t.Fatalf("expected affinity to match conn-account-1, got %s (ok=%v)", connID, ok)
	}
}

func TestAdaptiveRouterScoring(t *testing.T) {
	router := &AdaptiveRouter{
		stats: make(map[string]*ConnectionHealthStats),
	}

	connHealthy := &models.ProviderConnection{ID: "conn-healthy"}
	connDegraded := &models.ProviderConnection{ID: "conn-degraded"}
	connFatal := &models.ProviderConnection{ID: "conn-fatal"}

	// Record healthy stats (fast latency, high reliability)
	router.RecordSuccess(connHealthy.ID, 120*time.Millisecond)
	router.RecordSuccess(connHealthy.ID, 150*time.Millisecond)

	// Record degraded stats (high latency, failures)
	router.RecordFailure(connDegraded.ID, false)
	router.RecordSuccess(connDegraded.ID, 3500*time.Millisecond)

	// Record fatal error (tripped circuit breaker)
	router.RecordFailure(connFatal.ID, true)

	scoreHealthy := router.ScoreConnection(connHealthy)
	scoreDegraded := router.ScoreConnection(connDegraded)
	scoreFatal := router.ScoreConnection(connFatal)

	if scoreHealthy <= scoreDegraded {
		t.Fatalf("expected healthy conn score (%f) > degraded conn score (%f)", scoreHealthy, scoreDegraded)
	}

	if scoreFatal != 0.0 {
		t.Fatalf("expected fatal conn score to be 0 (quarantined), got %f", scoreFatal)
	}
}
