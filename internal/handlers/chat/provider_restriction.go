package chat

import (
	"fmt"
	"net/http"
	"strings"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/middleware"
)

// providerNotAllowedBody builds the 403 payload returned when the
// authenticated API key is restricted away from a provider.
func providerNotAllowedBody(provider string) []byte {
	return []byte(fmt.Sprintf(`{"error":{"message":"provider '%s' is not allowed for this API key","type":"permission_denied","code":403}}`, provider))
}

// writeProviderNotAllowed writes the 403 permission_denied payload
// for a provider the authenticated API key may not use.
func writeProviderNotAllowed(w http.ResponseWriter, provider string) {
	handlerutil.WriteJSON(w, http.StatusForbidden, map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("provider '%s' is not allowed for this API key", provider),
			"type":    "permission_denied",
			"code":    403,
		},
	})
}

// seatProviderID extracts the provider prefix of a "provider/model" entry.
func seatProviderID(entry string) string {
	if idx := strings.IndexByte(entry, '/'); idx > 0 {
		return entry[:idx]
	}
	return entry
}

// enforceProviderPermission gates a resolved model behind the
// authenticated API key's allowedProviders restriction. A combo is
// denied only when every seat is restricted away, so a restricted key
// can still reach the seats it is allowed to use; the per-seat dial
// itself is gated in tryForwardWithConnection.
func enforceProviderPermission(w http.ResponseWriter, r *http.Request, info *ModelInfo) bool {
	if info == nil {
		return true
	}
	apiKey := middleware.GetAuthenticatedApiKey(r)
	if len(info.ComboModels) == 0 {
		if !middleware.IsProviderAllowed(apiKey, info.Provider) {
			writeProviderNotAllowed(w, info.Provider)
			return false
		}
		return true
	}
	for _, seat := range info.ComboModels {
		if middleware.IsProviderAllowed(apiKey, seatProviderID(seat)) {
			return true
		}
	}
	writeProviderNotAllowed(w, seatProviderID(info.ComboModels[0]))
	return false
}

// filterModelsForAPIKey omits models whose owning provider the API key
// is not allowed to use. Combo entries are aggregates whose seats are
// gated at request time, so they stay listed; entries with no owner
// cannot be attributed and stay listed too.
func filterModelsForAPIKey(r *http.Request, data []ModelInfoObject) []ModelInfoObject {
	apiKey := middleware.GetAuthenticatedApiKey(r)
	if apiKey == nil || apiKey.AllowedProviders == nil {
		return data
	}
	filtered := make([]ModelInfoObject, 0, len(data))
	for _, m := range data {
		if m.OwnedBy == "" || m.OwnedBy == "combo" || middleware.IsProviderAllowed(apiKey, m.OwnedBy) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}
