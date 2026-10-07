package middleware

import (
	"9router/proxy/internal/log"
	"context"
	"net/http"
	"strings"

	"9router/proxy/internal/apikeycache"
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/models"
	"9router/proxy/internal/concurrency"
	"9router/proxy/internal/providers"
	"fmt"
)

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

// ApiKeyContextKey is the context key for the authenticated API key object.
const ApiKeyContextKey ContextKey = "apiKey"

// InvalidateApiKeyCache clears the in-memory API key cache. It must be called
// after any mutation of the apiKeys table so a revoked or reactivated key stops
// authenticating immediately instead of lingering for the cache TTL.
func InvalidateApiKeyCache() {
	apikeycache.Invalidate()
}

// resolveApiKey returns the API key object for key, preferring the in-memory
// cache and falling back to a database lookup on a miss. Only successful lookups
// are cached, so an unknown key does not have to re-hit SQLite on every attempt.
func resolveApiKey(repo *db.Repo, key string) (*models.APIKey, error) {
	if cached, ok := apikeycache.Lookup(key); ok {
		return cached, nil
	}

	apiKeyObj, err := repo.GetApiKeyByKey(key)
	if err != nil {
		return nil, err
	}
	if apiKeyObj != nil {
		apikeycache.Store(key, apiKeyObj)
	}
	return apiKeyObj, nil
}

// RequireApiKey creates a middleware handler that authenticates requests using client API keys.
// It checks the Authorization header (Bearer <key>) and the query parameter `key`.
// Valid keys are resolved from a short-lived in-memory cache backed by the SQLite
// database; inactive or disabled keys are rejected with 401.
func RequireApiKey(repo *db.Repo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKeyString := ExtractApiKey(r)
			if apiKeyString == "" {
				handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Authentication required. Provide an API key via Authorization: Bearer <key> header or ?key=<key> query parameter.")
				return
			}

			// Resolve via the in-memory cache, falling back to SQLite on a miss.
			apiKeyObj, err := resolveApiKey(repo, apiKeyString)
			if err != nil {
				log.Error("auth", "DB lookup error", "error", err)
				handlerutil.WriteJSONError(w, http.StatusInternalServerError, "Internal server error")
				return
			}
			if apiKeyObj == nil {
				handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Invalid API key.")
				return
			}

			if apiKeyObj.IsActive != 1 {
				handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Invalid or inactive API key.")
				return
			}

			// Concurrency limit enforcement per API key. When the limit is
			// reached we wait briefly (grace period) for a slot to free up
			// instead of rejecting with 429 immediately.
			if apiKeyObj.MaxConcurrent != nil && *apiKeyObj.MaxConcurrent > 0 {
				release, err := concurrency.GlobalLimiter.AcquireWithTimeout(r.Context(), apiKeyObj.Key, *apiKeyObj.MaxConcurrent, concurrency.DefaultAcquireTimeout)
				if err != nil {
					handlerutil.WriteJSONError(w, http.StatusTooManyRequests, fmt.Sprintf("Concurrent request limit exceeded for this API key (%d allowed)", *apiKeyObj.MaxConcurrent))
					return
				}
				defer release()
			}

			// Inject API Key info into the request context for downstream handlers/logging
			ctx := context.WithValue(r.Context(), ApiKeyContextKey, apiKeyObj)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAuthenticatedApiKey retrieves the authenticated APIKey object from the request context.
func GetAuthenticatedApiKey(r *http.Request) *models.APIKey {
	if r == nil {
		return nil
	}
	return GetAuthenticatedApiKeyFromContext(r.Context())
}

func GetAuthenticatedApiKeyFromContext(ctx context.Context) *models.APIKey {
	if ctx == nil {
		return nil
	}
	val := ctx.Value(ApiKeyContextKey)
	if val == nil {
		return nil
	}
	keyObj, ok := val.(*models.APIKey)
	if !ok {
		return nil
	}
	return keyObj
}

func IsProviderAllowed(apiKey *models.APIKey, provider string) bool {
	if apiKey == nil || apiKey.AllowedProviders == nil {
		return true
	}
	allowedStr := strings.TrimSpace(*apiKey.AllowedProviders)
	if allowedStr == "" {
		return true
	}
	target := strings.ToLower(strings.TrimSpace(provider))
	if target == "" {
		return false
	}
	canonTarget := strings.ToLower(providers.ResolveAlias(target))

	tokens := strings.Split(allowedStr, ",")
	for _, tok := range tokens {
		tok = strings.ToLower(strings.TrimSpace(tok))
		if tok == "" {
			continue
		}
		if tok == target || tok == canonTarget {
			return true
		}
		canonTok := strings.ToLower(providers.ResolveAlias(tok))
		if canonTok == target || canonTok == canonTarget {
			return true
		}
	}
	return false
}

// ExtractApiKey extracts the client API key from the request.
// For standard API routes, only header-based auth is accepted to avoid leaks.
// For EventSource / SSE stream endpoints (where browsers cannot set custom headers),
// query parameters `key` or `apiKey` are accepted as a fallback.
func ExtractApiKey(r *http.Request) string {
	// 1. Try Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return strings.TrimSpace(parts[1])
		}
	}

	// 2. Try custom X-API-Key header as fallback
	if xApiKey := r.Header.Get("X-API-Key"); xApiKey != "" {
		return xApiKey
	}

	// 3. For EventSource / SSE endpoints, accept query parameter key or apiKey
	if strings.HasSuffix(r.URL.Path, "/stream") {
		if qKey := r.URL.Query().Get("key"); qKey != "" {
			return qKey
		}
		if qKey := r.URL.Query().Get("apiKey"); qKey != "" {
			return qKey
		}
	}

	return ""
}
