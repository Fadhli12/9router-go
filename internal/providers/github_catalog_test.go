package providers

import (
	"slices"
	"testing"
)

// This fork preserves its own provider identifiers for the GitHub (Copilot)
// fallback catalog rather than adopting upstream's rename (gpt-5-mini,
// claude-fable-5, etc.). The two sides diverge deliberately; live account
// discovery stays authoritative regardless.

func TestGitHubCatalogRefresh(t *testing.T) {
	catalog := ProviderModels["github"]
	if catalog[0] != "gpt-5.2" {
		t.Errorf("github fallback default = %q, want gpt-5.2", catalog[0])
	}
	// Fork-specific identifiers that must remain served (preserved from upstream
	// history: see "fork preserves provider identifiers" merge commits).
	for _, want := range []string{
		"gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.4", "gpt-5.4-mini",
		"claude-haiku-4.5", "claude-opus-4.5", "claude-sonnet-4.5", "claude-sonnet-4.6",
		"claude-opus-4.6", "claude-opus-4.7", "gemini-2.5-pro", "gemini-3-flash-preview",
		"gemini-3.1-pro-preview", "grok-code-fast-1", "oswe-vscode-prime",
		"goldeneye-free-auto", "text-embedding-3-small", "text-embedding-ada-002",
	} {
		if !slices.Contains(catalog, want) {
			t.Errorf("github catalog lacks %q", want)
		}
	}
	if !slices.Equal(catalog, ProviderModels["gh"]) {
		t.Error("gh alias catalog diverged from github")
	}
}
