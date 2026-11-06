package providers

import (
	"net/http"
	"testing"
)

func TestLooksLikeAccountSuspension_Verdicts(t *testing.T) {
	tests := []struct {
		name   string
		status int
		text   string
	}{
		{
			name:   "antigravity consumer suspension",
			status: http.StatusForbidden,
			text:   `{"error":{"code":403,"message":"This account has been suspended for violating the Terms of Service.","status":"PERMISSION_DENIED"}}`,
		},
		{
			name:   "snake_case error code",
			status: http.StatusForbidden,
			text:   `{"error":{"type":"account_suspended","message":"forbidden"}}`,
		},
		{
			name:   "account disabled",
			status: http.StatusForbidden,
			text:   "Your account is disabled. Contact support.",
		},
		{
			name:   "account terminated",
			status: http.StatusForbidden,
			text:   "This account has been terminated.",
		},
		{
			name:   "banned wording",
			status: http.StatusForbidden,
			text:   "user has been banned from this service",
		},
		{
			name:   "permanently suspended on 401",
			status: http.StatusUnauthorized,
			text:   "account permanently suspended",
		},
		{
			name:   "mixed case is matched case-insensitively",
			status: http.StatusForbidden,
			text:   "ACCOUNT SUSPENDED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !LooksLikeAccountSuspension(tt.status, tt.text) {
				t.Errorf("LooksLikeAccountSuspension(%d, %q) = false, want true", tt.status, tt.text)
			}
		})
	}
}

// A false positive here halts every account of the provider, so each case below
// is a refusal that must stay per-account. The rate-limit and quota entries are
// the ones that would have turned one throttled account into a dead pool.
func TestLooksLikeAccountSuspension_NotAVerdict(t *testing.T) {
	tests := []struct {
		name   string
		status int
		text   string
	}{
		{
			name:   "empty body",
			status: http.StatusForbidden,
			text:   "",
		},
		{
			name:   "rate limit",
			status: http.StatusTooManyRequests,
			text:   "Rate limit exceeded. Please retry after 60s.",
		},
		{
			name:   "429 that mentions suspension is still a throttle",
			status: http.StatusTooManyRequests,
			text:   "too many requests; repeated abuse may get your account suspended",
		},
		{
			name:   "quota exhausted",
			status: http.StatusPaymentRequired,
			text:   "You have exceeded your current quota. Please check your plan and billing details.",
		},
		{
			name:   "insufficient credits",
			status: http.StatusPaymentRequired,
			text:   "Insufficient credits to complete this request",
		},
		{
			name:   "token expired",
			status: http.StatusUnauthorized,
			text:   "Access token has expired. Please refresh.",
		},
		{
			name:   "invalid grant",
			status: http.StatusUnauthorized,
			text:   `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`,
		},
		{
			name:   "invalid api key",
			status: http.StatusUnauthorized,
			text:   "Incorrect API key provided",
		},
		{
			name:   "plain unauthorized",
			status: http.StatusUnauthorized,
			text:   "Unauthorized",
		},
		{
			name:   "permission denied without a verdict",
			status: http.StatusForbidden,
			text:   `{"error":{"code":403,"message":"Permission denied on resource project.","status":"PERMISSION_DENIED"}}`,
		},
		{
			name:   "model not found",
			status: http.StatusNotFound,
			text:   "The model `gpt-9` does not exist or you do not have access to it.",
		},
		{
			name:   "advisory naming suspension as a possible cause",
			status: http.StatusUnauthorized,
			text:   "Authentication failed. Verify your key is valid and that your account is not suspended.",
		},
		{
			name:   "negated: account no longer suspended",
			status: http.StatusForbidden,
			text:   "your account is no longer suspended, retry the request",
		},
		{
			name:   "upstream fault echoing the word in a template",
			status: http.StatusInternalServerError,
			text:   "internal error rendering template account_suspended.html",
		},
		{
			name:   "bad gateway",
			status: http.StatusBadGateway,
			text:   "upstream connect error: account banned handler crashed",
		},
		{
			name:   "model capacity exhausted",
			status: http.StatusServiceUnavailable,
			text:   "No capacity available for model MODEL_CAPACITY_EXHAUSTED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if LooksLikeAccountSuspension(tt.status, tt.text) {
				t.Errorf("LooksLikeAccountSuspension(%d, %q) = true, want false", tt.status, tt.text)
			}
		})
	}
}

// The two predicates answer different questions and must not be collapsed:
// LooksLikePermanentFailure says the credential is dead (repair that account),
// LooksLikeAccountSuspension says the account was punished (halt the provider).
func TestLooksLikeAccountSuspension_NarrowerThanPermanentFailure(t *testing.T) {
	permanentButNotSuspension := []string{
		"Unauthorized",
		"token expired",
		"invalid_grant",
		"Incorrect API key provided",
		"permission denied",
		"not found",
		"no credentials",
	}

	for _, text := range permanentButNotSuspension {
		t.Run(text, func(t *testing.T) {
			if !LooksLikePermanentFailure(text) {
				t.Fatalf("LooksLikePermanentFailure(%q) = false, want true (fixture no longer represents a permanent failure)", text)
			}
			if LooksLikeAccountSuspension(http.StatusUnauthorized, text) {
				t.Errorf("LooksLikeAccountSuspension(401, %q) = true, want false: a dead credential must not halt the provider", text)
			}
		})
	}
}

// realAntigravityBanBody is the message an operator's Antigravity accounts
// actually came back with on 2026-11-06. It is pinned verbatim because the
// pattern list was written from guessed wording and missed it: every entry
// looked for "account disabled", while the upstream scopes the subject to the
// service — "disabled in this account".
const realAntigravityBanBody = "This service has been disabled in this account for violation of Terms of Service. Please submit an appeal to continue using this product."

func TestRealAntigravityBanIsDetected(t *testing.T) {
	for _, status := range []int{
		http.StatusForbidden,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			if !LooksLikeAccountSuspension(status, realAntigravityBanBody) {
				t.Errorf("the live Antigravity ban message was not detected at status %d", status)
			}
		})
	}

	// Still a throttle at 429, whatever the body says.
	if LooksLikeAccountSuspension(http.StatusTooManyRequests, realAntigravityBanBody) {
		t.Error("429 carrying the ban wording was treated as a verdict")
	}
	// A JSON envelope is the shape the executor actually passes.
	wrapped := `{"error":{"code":403,"message":"` + realAntigravityBanBody + `","status":"PERMISSION_DENIED"}}`
	if !LooksLikeAccountSuspension(http.StatusForbidden, wrapped) {
		t.Error("the ban message inside a JSON envelope was not detected")
	}
}

// A content-moderation refusal uses the same Terms-of-Service vocabulary as the
// ban, but it is a verdict on ONE REQUEST. Matching it would let a single
// blocked prompt halt the provider and take every healthy account with it,
// which is why the ToS phrasing itself is not a pattern.
func TestModerationRefusalDoesNotHaltTheProvider(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{
			name: "request blocked for ToS violation",
			text: "Your request was blocked for violation of Terms of Service.",
		},
		{
			name: "content violates ToS",
			text: "This content violates the Terms of Service and cannot be processed.",
		},
		{
			name: "safety filter cites ToS",
			text: "Prompt blocked: violation of terms of service (safety filter).",
		},
		{
			name: "rejection invites an appeal",
			text: "Request rejected. If you believe this is an error, submit an appeal.",
		},
		{
			name: "service disabled without naming the account",
			text: "This service has been disabled. Try again later.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if LooksLikeAccountSuspension(http.StatusForbidden, tt.text) {
				t.Errorf("per-request refusal halted the provider: %q", tt.text)
			}
		})
	}
}
