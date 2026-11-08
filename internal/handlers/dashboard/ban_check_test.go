package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/handlers/chat"
	"9router/proxy/internal/providers"
)

// A suspended Google account still holds a valid OAuth token and refreshes it
// happily, so the existing connection probe reports success on an account that
// cannot serve one request. The ban check exists because only a real request
// answers "has the appeal been granted?".
//
// It is also a real request on the fingerprint this build sends, which is why
// the drift guard matters more than the feature: an account that has spent its
// one appeal cannot survive a second verdict, so a check that could re-ban the
// account it clears would be worse than no check at all.

func TestAntigravityDriftForGuard(t *testing.T) {
	t.Run("no release observed yet allows the check", func(t *testing.T) {
		providers.ResetAntigravityRelease()
		t.Cleanup(providers.ResetAntigravityRelease)

		// Not knowing is not the same as knowing it is stale: blocking on a
		// missing signal would make the button dead on a fresh boot.
		if d := antigravityDriftFor("antigravity"); d != nil {
			t.Errorf("drift = %+v with nothing observed, want nil", d)
		}
	})

	t.Run("a non-antigravity provider is never guarded", func(t *testing.T) {
		providers.ResetAntigravityRelease()
		t.Cleanup(providers.ResetAntigravityRelease)

		if d := antigravityDriftFor("openai"); d != nil {
			t.Errorf("drift = %+v for openai, want nil", d)
		}
	})
}

func TestProviderServesModel(t *testing.T) {
	t.Run("a model in the provider catalog is served", func(t *testing.T) {
		models := providers.GetProviderModels("antigravity")
		if len(models) == 0 {
			t.Skip("antigravity catalog is empty in this build")
		}
		if !providerServesModel("antigravity", models[0]) {
			t.Errorf("%s reported as not served by antigravity", models[0])
		}
	})

	t.Run("a foreign model is refused", func(t *testing.T) {
		// A ban check on a model the account cannot serve fails for the wrong
		// reason, and that failure reads as a ban.
		if providerServesModel("antigravity", "gpt-4o-this-is-not-a-gemini-model") {
			t.Error("a foreign model was reported as served")
		}
	})

	t.Run("an unknown provider is accepted", func(t *testing.T) {
		// A custom node's models are not in the static catalog; its upstream is
		// the authority on what it serves.
		if !providerServesModel("openai-compatible-chat-whatever", "some-model") {
			t.Error("a custom node's model was refused")
		}
	})
}

func TestBanCheckBodyIsMinimal(t *testing.T) {
	body := banCheckBody("gemini-3.8-flash")

	var payload struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Stream    bool   `json:"stream"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if payload.Model != "gemini-3.8-flash" {
		t.Errorf("model = %q, want gemini-3.8-flash", payload.Model)
	}
	// A ban refusal arrives before any tokens are generated, so one token is
	// enough to learn the verdict without paying for a reply.
	if payload.MaxTokens != 1 {
		t.Errorf("max_tokens = %d, want 1", payload.MaxTokens)
	}
	if payload.Stream {
		t.Error("stream = true; a ban check reads one verdict, not a stream")
	}
	if len(payload.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(payload.Messages))
	}
}

// The verdict mapping is what the operator reads, so each upstream outcome has
// to land on the right state. "error" must never be reported as "lifted": that
// would invite switching a still-banned account back on.
func TestBanCheckVerdictStates(t *testing.T) {
	tests := []struct {
		name      string
		verdict   chat.BanCheckVerdict
		wantState string
	}{
		{
			name:      "a suspension refusal is banned",
			verdict:   chat.BanCheckVerdict{Banned: true, Status: 403, Message: "This service has been disabled in this account for violation of Terms of Service."},
			wantState: banCheckStateBanned,
		},
		{
			name:      "a successful request is lifted",
			verdict:   chat.BanCheckVerdict{Lifted: true, Status: 200},
			wantState: banCheckStateLifted,
		},
		{
			name:      "any other refusal is an error, not lifted",
			verdict:   chat.BanCheckVerdict{Status: 429, Message: "rate limited"},
			wantState: banCheckStateError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := banCheckStateFor(tt.verdict); got != tt.wantState {
				t.Errorf("banCheckStateFor(%+v) = %q, want %q", tt.verdict, got, tt.wantState)
			}
		})
	}
}

// A handler with no chat engine must say so rather than answer "lifted" for a
// check it never ran.
func TestHandleBanCheckWithoutEngine(t *testing.T) {
	h := &DashboardHandler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/connections/abc/ban-check",
		strings.NewReader(`{"model":"gemini-3.8-flash"}`))

	h.HandleBanCheck(rec, req)

	// No repo either, so this cannot reach the engine check; what matters is
	// that it refuses rather than reporting a verdict.
	if rec.Code == http.StatusOK {
		t.Errorf("status = 200 with no engine and no connection; a verdict was invented")
	}
}

// The guard is the whole safety claim: with the fingerprint stale, a ban check
// is a real request that can re-ban the account it was meant to clear, and an
// account that has spent its one appeal cannot survive that. These drive the
// refusal decision directly.
func TestAntigravityDriftRefusesWhileStale(t *testing.T) {
	seen := time.Now().UTC()

	t.Run("a newer release refuses the check", func(t *testing.T) {
		d := antigravityDriftFrom("antigravity",
			providers.AntigravityRelease{Version: "99.0.0", RolloutPercent: 100}, seen)
		if d == nil {
			t.Fatal("drift = nil with a newer release; the check would run on a stale fingerprint")
		}
		if d.Released != "99.0.0" {
			t.Errorf("Released = %q, want 99.0.0", d.Released)
		}
		if d.Pinned != providers.AntigravityCLIVersion {
			t.Errorf("Pinned = %q, want %q", d.Pinned, providers.AntigravityCLIVersion)
		}
	})

	t.Run("the pinned version allows the check", func(t *testing.T) {
		if d := antigravityDriftFrom("antigravity",
			providers.AntigravityRelease{Version: providers.AntigravityCLIVersion}, seen); d != nil {
			t.Errorf("drift = %+v when the identity matches; the button would be dead for no reason", d)
		}
	})

	t.Run("an older release allows the check", func(t *testing.T) {
		if d := antigravityDriftFrom("antigravity",
			providers.AntigravityRelease{Version: "0.0.1"}, seen); d != nil {
			t.Errorf("drift = %+v for an older release, want nil", d)
		}
	})

	t.Run("the ag alias is guarded too", func(t *testing.T) {
		if d := antigravityDriftFrom("ag",
			providers.AntigravityRelease{Version: "99.0.0"}, seen); d == nil {
			t.Error("the ag alias was not guarded; it resolves to the same provider")
		}
	})
}
