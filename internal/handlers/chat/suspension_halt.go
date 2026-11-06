package chat

import (
	json "encoding/json/v2"
	"fmt"
	"net/http"

	"9router/proxy/internal/log"
	"9router/proxy/internal/models"
	"9router/proxy/internal/providers"
	"9router/proxy/internal/resilience"
)

// errProviderSuspended is the resolver's refusal, for callers that take an
// error rather than an *upstreamError.
//
// It names the accounts so an operator reading a media handler's log sees which
// one to appeal, and it says what releases the halt — a client that only saw
// "no connections" would read a deliberate halt as a misconfiguration.
func errProviderSuspended(provider string) error {
	canonical := providers.ResolveAlias(provider)
	rec, halted := resilience.SuspensionFor(canonical)
	if !halted {
		return fmt.Errorf("provider %s is halted", canonical)
	}
	return fmt.Errorf(
		"provider %s is halted: upstream suspended account(s) %v (%q). The remaining accounts are deliberately not being spent; clear the halt after appealing",
		canonical, rec.Accounts, rec.Reason)
}

// suspendedProviderError is what a client gets once the provider is halted, or
// nil when it is not.
//
// 403 rather than 503: the gateway is not failing and waiting will not help, so
// a client must not schedule a retry. The account that earned the verdict is
// named so an operator reading the client's own log knows which one to appeal.
func suspendedProviderError(provider string) *upstreamError {
	rec, halted := resilience.SuspensionFor(providers.ResolveAlias(provider))
	if !halted {
		return nil
	}
	payload := map[string]any{
		"error": map[string]any{
			"message":  "provider halted: upstream suspended an account, so the remaining accounts are not being spent. Clear the halt after appealing.",
			"type":     "provider_suspended",
			"provider": rec.Provider,
			"accounts": rec.Accounts,
			"upstream": rec.Reason,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		body = []byte(`{"error":{"message":"provider halted: upstream suspended an account","type":"provider_suspended"}}`)
	}
	return &upstreamError{StatusCode: http.StatusForbidden, Body: body}
}

// haltProviderOnSuspension latches the provider and records the refusal against
// the account that earned it.
//
// The account still carries the error for the dashboard: the suspension is a
// fact about that credential too, and an operator who clears the latch needs to
// see which account was refused.
func (h *ChatHandler) haltProviderOnSuspension(provider, model string, conn *models.ProviderConnection, ue *upstreamError) {
	canonical := providers.ResolveAlias(provider)
	errText := extractErrorText(ue.Body)

	connID := ""
	if conn != nil {
		connID = conn.ID
	}

	latched := resilience.RecordAccountSuspension(canonical, connID, ue.StatusCode, errText)

	if connID != "" && h.Repo != nil {
		if err := h.Repo.RecordConnectionScopedError(connID, model, "chat", ue.StatusCode, errText, 0); err != nil {
			log.Warn("fallback", "record suspension on connection failed", "conn", connID, "error", err)
		}
		h.getSessionAffinity().EvictConnection(connID)
	}

	// Switching the connections off is what makes the halt visible: the
	// dashboard's per-provider toggle reads isActive, so a latch that only
	// lived in memory left the provider looking enabled while every request
	// was refused. Re-enabling from that same toggle is the manual release.
	//
	// Only once the latch is set. Below the threshold the provider is still
	// serving, and turning its accounts off would halt it early.
	deactivated := int64(0)
	if latched && h.Repo != nil {
		var err error
		if deactivated, err = h.Repo.DeactivateProviderConnections(canonical); err != nil {
			// The in-memory latch already refuses every request, so a failed
			// write costs visibility, not protection.
			log.Warn("fallback", "could not switch the halted provider off in the dashboard",
				"provider", canonical, "error", err)
		}
	}

	kv := []any{
		"provider", canonical, "model", model, "conn", connID,
		"status", ue.StatusCode, "latched", latched, "accountsDisabled", deactivated,
	}
	log.Error("fallback", "account suspended by upstream; halting the provider until an operator clears it",
		append(kv, connIdentityKV(conn)...)...)
}
