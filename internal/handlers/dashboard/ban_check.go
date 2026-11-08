package dashboard

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"time"

	"9router/proxy/internal/handlers/chat"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/providers"
)

// Per-account ban check.
//
// An Antigravity account that Google suspended still holds a valid OAuth token
// and refreshes it happily, so "Test connection" reports success on an account
// that cannot serve a single request: probeOAuthConnection only asks whether a
// token exists. The only way to learn whether an appeal has been granted is to
// send one real request on that one credential, which is what an operator was
// doing by hand — switch the account on, call a model, switch it back off.
//
// This endpoint does that in one click, pinned to the named account, and
// refuses to do it while the gateway's Antigravity fingerprint is stale.

// ChatBanProber is the one chat-engine capability this endpoint needs. Declared
// here as an interface so the dashboard package does not import the chat
// package for a single method.
type ChatBanProber interface {
	CheckConnectionBan(ctx context.Context, provider, model, connectionID string, body []byte) (chat.BanCheckVerdict, error)
}

// BanCheckRequest names the account to ask about and the model to ask with.
type BanCheckRequest struct {
	// Model is the model id to send. The dashboard offers only the provider's
	// own models, and the operator picks a cheap one — a ban check is a real
	// billable request.
	Model string `json:"model"`
}

// BanCheckResponse is the verdict for one account.
type BanCheckResponse struct {
	// State is "banned", "lifted", "error", or "refused".
	State string `json:"state"`
	// Status is the upstream HTTP status, 0 when the request never reached it.
	Status int `json:"status,omitempty"`
	// Message is the upstream's own wording. Google names the appeal in it, so
	// it is shown verbatim rather than summarised.
	Message string `json:"message,omitempty"`
	// Model echoes what was actually sent, so a verdict cannot be misread as
	// belonging to a different model.
	Model string `json:"model,omitempty"`
	// Drift carries the version mismatch when State is "refused".
	Drift *BanCheckDrift `json:"drift,omitempty"`
}

// BanCheckDrift explains a refusal: the client release Google serves against
// the identity this build sends.
type BanCheckDrift struct {
	Released string `json:"released"`
	Pinned   string `json:"pinned"`
}

// banCheckStates are the response's closed set of outcomes.
const (
	banCheckStateBanned  = "banned"
	banCheckStateLifted  = "lifted"
	banCheckStateError   = "error"
	banCheckStateRefused = "refused"
)

// HandleBanCheck handles POST /api/connections/{id}/ban-check.
func (h *DashboardHandler) HandleBanCheck(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing connection id")
		return
	}
	conn, err := h.Repo.GetProviderConnectionByID(id)
	if err != nil || conn == nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "connection not found")
		return
	}

	var req BanCheckRequest
	if body, readErr := io.ReadAll(r.Body); readErr == nil && len(body) > 0 {
		_ = json.Unmarshal(body, &req)
	}
	defer r.Body.Close()

	model := strings.TrimSpace(req.Model)
	if model == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "model required: pick one of the provider's models")
		return
	}
	// The model has to belong to this provider. A ban check on a model the
	// account cannot serve would fail for the wrong reason and read as a ban.
	if !providerServesModel(conn.Provider, model) {
		handlerutil.WriteJSONError(w, http.StatusBadRequest,
			"model "+model+" is not served by provider "+conn.Provider)
		return
	}

	// The guard. A ban check is a real request on the fingerprint this build
	// sends, so running it while that fingerprint is stale risks the very ban
	// it is asking about — and an account that has already spent its one appeal
	// cannot survive a second verdict. Refusing is the whole point: a check
	// that can re-ban the account it clears would be worse than no check.
	if drift := antigravityDriftFor(conn.Provider); drift != nil {
		handlerutil.WriteJSON(w, http.StatusConflict, BanCheckResponse{
			State: banCheckStateRefused,
			Message: "the gateway's Antigravity identity is stale (released " + drift.Released +
				", this build sends " + drift.Pinned + "). A ban check is a real request on that " +
				"fingerprint, so it is refused until the pinned identity is patched.",
			Drift: drift,
		})
		return
	}

	if h.ChatProbe == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "chat engine not wired")
		return
	}

	verdict, probeErr := h.ChatProbe.CheckConnectionBan(r.Context(), conn.Provider, model, conn.ID, banCheckBody(model))
	if probeErr != nil {
		handlerutil.WriteJSON(w, http.StatusOK, BanCheckResponse{
			State:   banCheckStateError,
			Message: probeErr.Error(),
			Model:   model,
		})
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, BanCheckResponse{
		State:   banCheckStateFor(verdict),
		Status:  verdict.Status,
		Message: verdict.Message,
		Model:   model,
	})
}

// banCheckStateFor maps one upstream outcome to the state the operator reads.
//
// Anything that is not an explicit suspension and not an explicit success is an
// error, never "lifted": reporting a rate limit or a network fault as lifted
// would invite switching a still-banned account back on.
func banCheckStateFor(v chat.BanCheckVerdict) string {
	switch {
	case v.Banned:
		return banCheckStateBanned
	case v.Lifted:
		return banCheckStateLifted
	default:
		return banCheckStateError
	}
}

// antigravityDriftFor reports the version mismatch when the provider is
// Antigravity and a newer client has been released, else nil.
//
// Scoped to Antigravity because that is the provider whose identity this
// gateway pins and whose accounts were lost to a stale one.
func antigravityDriftFor(provider string) *BanCheckDrift {
	rel, seen := providers.LastAntigravityRelease()
	return antigravityDriftFrom(provider, rel, seen)
}

// antigravityDriftFrom is antigravityDriftFor with the observed release passed
// in, so a test drives the refusal path without reaching into package state.
func antigravityDriftFrom(provider string, rel providers.AntigravityRelease, seen time.Time) *BanCheckDrift {
	if providers.ResolveAlias(provider) != "antigravity" {
		return nil
	}
	if seen.IsZero() || rel.Version == "" {
		// Nothing observed yet: the watch has not polled, or the updater's
		// wording changed. Not knowing is not the same as knowing it is stale,
		// so the check is allowed rather than blocked on a missing signal.
		return nil
	}
	d := providers.AntigravityReleaseDrift(rel)
	if !d.Newer {
		return nil
	}
	return &BanCheckDrift{Released: d.Observed, Pinned: d.Pinned}
}

// providerServesModel reports whether the provider's catalog carries the model.
func providerServesModel(provider, model string) bool {
	catalog := providers.GetProviderModels(provider)
	// A custom node's models are not in the static catalog, so a provider the
	// catalog does not know is accepted rather than refused — its upstream is
	// the authority on what it serves.
	if len(catalog) == 0 {
		return true
	}
	for _, m := range catalog {
		if strings.EqualFold(m, model) {
			return true
		}
	}
	return false
}

// banCheckBody is the smallest request that still reaches the model: a ban
// refusal arrives before any tokens are generated, so one token of output is
// enough to learn the verdict without paying for a reply.
func banCheckBody(model string) []byte {
	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 1,
		"stream":     false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`)
	}
	return body
}
