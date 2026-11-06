package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"strings"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/log"
	"9router/proxy/internal/providers"
	"9router/proxy/internal/resilience"
)

// Provider-level resilience status for the dashboard.
//
// The numbers come from the same in-memory layer the request path consults, so
// what an operator sees here is what a request would have been judged against —
// not a second estimate that can drift from the gate.
type ResilienceStatusResponse struct {
	Breakers    []BreakerStatusResponse    `json:"breakers"`
	Semaphore   []SemaphoreStatResponse    `json:"semaphore"`
	Suspensions []SuspensionStatusResponse `json:"suspensions"`
}

// SuspensionStatusResponse is one halted provider.
//
// Unlike a breaker, this never clears on its own, so the dashboard has to show
// it: a provider halted by an account suspension stays halted until an
// operator clears it, and an invisible halt looks exactly like an outage.
type SuspensionStatusResponse struct {
	Provider    string   `json:"provider"`
	StatusCode  int      `json:"statusCode"`
	Reason      string   `json:"reason"`
	Accounts    []string `json:"accounts"`
	FirstSeenAt string   `json:"firstSeenAt"`
	LastSeenAt  string   `json:"lastSeenAt"`
}

// BreakerStatusResponse is one provider's circuit breaker snapshot.
type BreakerStatusResponse struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	FailureCount int    `json:"failureCount"`
	SuccessCount int    `json:"successCount"`
	RetryAfterMs int64  `json:"retryAfterMs"`
	OpenedAt     string `json:"openedAt,omitempty"`
}

// SemaphoreStatResponse is one account gate snapshot.
type SemaphoreStatResponse struct {
	Key            string `json:"key"`
	Running        int    `json:"running"`
	Queued         int    `json:"queued"`
	MaxConcurrency int    `json:"maxConcurrency"`
	BlockedUntil   string `json:"blockedUntil,omitempty"`
}

// HandleGetResilience handles GET /api/resilience.
//
// Answers "is any provider being held back right now, and is any account
// queuing?" — the question an operator asks when throughput drops and the
// per-provider error rate looks normal. Without this the breaker is invisible:
// requests simply fail with a 503 and nothing says the gateway stopped dialling
// a provider on purpose.
func (h *DashboardHandler) HandleGetResilience(w http.ResponseWriter, r *http.Request) {
	resp := ResilienceStatusResponse{
		Breakers:    []BreakerStatusResponse{},
		Semaphore:   []SemaphoreStatResponse{},
		Suspensions: []SuspensionStatusResponse{},
	}
	// Read outside the controller nil check: the halt latch is package state,
	// so a dashboard on a gateway without a wired controller must still show
	// the halt rather than report an empty, healthy-looking panel.
	for _, rec := range resilience.AllSuspensions() {
		resp.Suspensions = append(resp.Suspensions, SuspensionStatusResponse{
			Provider:    rec.Provider,
			StatusCode:  rec.StatusCode,
			Reason:      rec.Reason,
			Accounts:    rec.Accounts,
			FirstSeenAt: rec.FirstSeenAt,
			LastSeenAt:  rec.LastSeenAt,
		})
	}
	if h.Resilience != nil {
		breakers, gates := h.Resilience.Statuses()
		resp.Breakers = make([]BreakerStatusResponse, 0, len(breakers))
		for _, b := range breakers {
			resp.Breakers = append(resp.Breakers, BreakerStatusResponse{
				Name:         b.Name,
				State:        string(b.State),
				FailureCount: b.FailureCount,
				SuccessCount: b.SuccessCount,
				RetryAfterMs: b.RetryAfterMs,
				OpenedAt:     b.OpenedAt,
			})
		}
		resp.Semaphore = make([]SemaphoreStatResponse, 0, len(gates))
		for _, g := range gates {
			resp.Semaphore = append(resp.Semaphore, SemaphoreStatResponse(g))
		}
	}
	handlerutil.WriteJSON(w, http.StatusOK, resp)
}

// HandleResetResilience handles POST /api/resilience/reset.
//
// Closes every breaker and clears every gate. Used when an operator has fixed a
// provider and does not want to wait out the reset timeout, and by tests.
func (h *DashboardHandler) HandleResetResilience(w http.ResponseWriter, r *http.Request) {
	if h.Resilience == nil {
		handlerutil.WriteJSONError(w, http.StatusNotImplemented, "resilience layer not wired")
		return
	}
	h.Resilience.Reset()
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// ClearSuspensionRequest names the provider to release.
type ClearSuspensionRequest struct {
	Provider string `json:"provider"`
}

// HandleClearSuspension handles POST /api/resilience/suspensions/clear.
//
// This is the only way a halted provider resumes, and it is deliberately
// separate from /resilience/reset: that button closes breakers an operator has
// already fixed, while this one asserts an appeal succeeded. Folding the halt
// into the reset would let a routine click resume the cascade the latch exists
// to stop.
//
// The provider must be named — there is no "clear all", because two providers
// halted for different reasons need two separate appeals.
func (h *DashboardHandler) HandleClearSuspension(w http.ResponseWriter, r *http.Request) {
	var req ClearSuspensionRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "provider is required")
		return
	}
	canonical := providers.ResolveAlias(provider)
	if _, halted := resilience.SuspensionFor(canonical); !halted {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "provider is not halted")
		return
	}
	if err := resilience.ClearSuspension(canonical); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "clear suspension failed: "+err.Error())
		return
	}
	log.Warn("resilience", "operator cleared a provider halt", "provider", canonical)
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "provider": canonical})
}

// ensure the resilience package stays a direct dependency of this handler: the
// field below is what the two endpoints read, and a build that drops the import
// should fail here rather than at the call site.
var _ = resilience.DefaultController
