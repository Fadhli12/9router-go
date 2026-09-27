package providers

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// upstreamLevelsFixture is what open-sse/providers/thinkingLevels.js returns for
// every (provider, model) pair in the Go model catalog, captured from the
// upstream checkout. It pins the port against the real implementation instead of
// a hand-written subset.
var upstreamLevelsFixture = "testdata/thinking_levels.json"

// TestGetThinkingLevels_MatchesUpstreamFixture compares the Go resolver against
// the upstream one for the whole catalog. A diff here means a level set, pattern
// row or capability declaration drifted; the dashboard picker would then offer a
// level the provider rejects, or hide one it accepts.
func TestGetThinkingLevels_MatchesUpstreamFixture(t *testing.T) {
	raw, err := os.ReadFile(upstreamLevelsFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var want map[string]map[string][]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	var checked, mismatched int
	for provider, models := range want {
		for model, wantLevels := range models {
			checked++
			InvalidateCapabilitiesCache()
			got := GetThinkingLevels(provider, model)

			if !slices.Equal(got, wantLevels) {
				mismatched++
				if mismatched <= 20 {
					t.Errorf("%s/%s: got %v, upstream %v", provider, model, got, wantLevels)
				}
			}
		}
	}

	if mismatched > 0 {
		t.Errorf("%d of %d (provider, model) pairs diverge from upstream", mismatched, checked)
	}
	t.Logf("verified %d (provider, model) pairs against %s", checked, upstreamLevelsFixture)
}
