// Package oauth provides per-provider OAuth token refresh.
// Register custom refresh functions for providers that need non-standard OAuth flows.
package oauth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// TokenResult holds the result of a token refresh.
type TokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // seconds
	Scope        string
	ProjectID    string // provider-specific extra field
}

// Params holds all inputs for a refresh call.
type Params struct {
	Client       *http.Client
	Provider     string
	RefreshToken string
	AccessToken  string // current (possibly expired) token
	// ConnectionID identifies the stored connection being refreshed. When set,
	// it keys singleflight deduplication so concurrent refreshes for the same
	// connection coalesce into one upstream OAuth call.
	ConnectionID string
	// ProviderSpecificData carries per-account OAuth material stored at login
	// time (e.g. Kiro clientId/clientSecret/region for AWS SSO OIDC refresh).
	ProviderSpecificData map[string]string
}

// Refresher refreshes an OAuth token for a specific provider.
type Refresher func(ctx context.Context, p *Params) (*TokenResult, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Refresher{}

	// DefaultOAuthTransport is an optimized HTTP transport with connection pooling and keep-alives for OAuth.
	DefaultOAuthTransport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}

	// DefaultTransport is an alias for DefaultOAuthTransport.
	DefaultTransport = DefaultOAuthTransport

	// DefaultOAuthClient is the shared optimized HTTP client for OAuth token refreshes with keep-alive transport.
	DefaultOAuthClient = &http.Client{
		Transport: DefaultOAuthTransport,
		Timeout:   30 * time.Second,
	}

	// DefaultClient is an alias for DefaultOAuthClient.
	DefaultClient = DefaultOAuthClient

	refreshFlight singleflight.Group
)

// Register adds a refresher for the given provider.
func Register(provider string, fn Refresher) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[provider] = fn
}

// Get returns the refresher for the given provider, or nil.
func Get(provider string) Refresher {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[provider]
}

// Unregister removes the refresher for a provider, restoring the entry to
// "none registered". Tests that register a stub under a real provider id need
// this to put the process-wide registry back the way they found it — a leaked
// stub silently changes what production code under test does.
func Unregister(provider string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, provider)
}

// Refresh calls the provider's refresher, or falls back to standard OAuth2.
// Concurrent calls for the same provider and refresh token are deduplicated via singleflight.
func Refresh(ctx context.Context, p *Params) (*TokenResult, error) {
	if p == nil {
		return nil, fmt.Errorf("oauth: params cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fn := Get(p.Provider)
	if fn == nil {
		return nil, fmt.Errorf("no OAuth refresher for: %s", p.Provider)
	}

	pCopy := *p
	if pCopy.Client == nil {
		pCopy.Client = DefaultOAuthClient
	}

	if pCopy.RefreshToken == "" {
		return fn(ctx, &pCopy)
	}

	key := pCopy.Provider + ":" + pCopy.RefreshToken
	if pCopy.ConnectionID != "" {
		key = "conn:" + pCopy.ConnectionID
	}
	res, err, _ := refreshFlight.Do(key, func() (any, error) {
		return fn(ctx, &pCopy)
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	tr, ok := res.(*TokenResult)
	if !ok || tr == nil {
		return nil, fmt.Errorf("oauth: unexpected token result type")
	}
	resultCopy := *tr
	return &resultCopy, nil
}

// StringMap flattens a connection providerSpecificData blob to the string
// fields refreshers need (clientId/clientSecret/region). Non-string values
// are dropped.
func StringMap(m map[string]any) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok && s != "" {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
