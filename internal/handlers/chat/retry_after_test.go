package chat

import (
	"net/http"
	"testing"
	"time"
)

// TestRetryableCooldownSec_429Floor verifies the smart cooldown floor for
// rate-limited (429) upstreams: whatever Retry-After or the classifier's own
// backoff names, the account is never parked for less than
// minRateLimitCooldownSec (30s), so a short-lived burst limit cannot re-pick the
// same still-limited account on the very next failover pass.
func TestRetryableCooldownSec_429Floor(t *testing.T) {
	tests := []struct {
		name     string
		base     time.Duration
		ue       *upstreamError
		wantSec  int
	}{
		{
			name:    "classifier backoff below floor is lifted to 30s",
			base:    2 * time.Second,
			ue:      &upstreamError{StatusCode: http.StatusTooManyRequests, Body: []byte(`{"error":{"message":"rate limited"}}`)},
			wantSec: minRateLimitCooldownSec,
		},
		{
			name:    "short Retry-After below floor is lifted to 30s",
			base:    2 * time.Second,
			ue:      &upstreamError{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"5"}}},
			wantSec: minRateLimitCooldownSec,
		},
		{
			name:    "no carrier at all still floors at 30s",
			base:    500 * time.Millisecond,
			ue:      &upstreamError{StatusCode: http.StatusTooManyRequests},
			wantSec: minRateLimitCooldownSec,
		},
		{
			name:    "Retry-After above floor is honoured",
			base:    2 * time.Second,
			ue:      &upstreamError{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"60"}}},
			wantSec: 60,
		},
		{
			name:    "RetryInfo delay above floor is honoured",
			base:    2 * time.Second,
			ue:      &upstreamError{
				StatusCode: http.StatusTooManyRequests,
				Body:       []byte(`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"45s"}]}}`),
			},
			wantSec: 45,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := retryableCooldownSec(http.StatusTooManyRequests, tt.base, tt.ue)
			if got != tt.wantSec {
				t.Errorf("retryableCooldownSec() = %d, want %d", got, tt.wantSec)
			}
		})
	}
}

// TestRetryableCooldownSec_Non429Unchanged guards the non-429 path: it must
// keep its quota-reset reading and must never apply the 30s rate-limit floor.
func TestRetryableCooldownSec_Non429Unchanged(t *testing.T) {
	base := 2 * time.Second

	// No reset carrier: cooldown falls back to the classifier's base, clamped
	// only by maxResetCooldown (never lifted to 30s).
	got := retryableCooldownSec(http.StatusInternalServerError, base, &upstreamError{StatusCode: http.StatusInternalServerError})
	if got != int(ceilSeconds(base)) {
		t.Errorf("expected non-429 cooldown %d, got %d", int(ceilSeconds(base)), got)
	}

	// A 429 floor must NOT leak into a 5xx path with the same base.
	if got >= minRateLimitCooldownSec {
		t.Errorf("non-429 cooldown %d unexpectedly reached the 429 floor %d", got, minRateLimitCooldownSec)
	}
}

func TestMonthlyQuotaCooldownSec(t *testing.T) {
	// Jan 15 -> Feb 1. The exact span depends on the month, so pin the two
	// boundaries rather than a hard-coded second count.
	jan := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	got := monthlyQuotaCooldownSec(jan)
	feb1 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	want := int(feb1.Sub(jan) / time.Second)
	if got != want {
		t.Errorf("monthlyQuotaCooldownSec(%v) = %d, want %d", jan, got, want)
	}

	// Dec 31 -> Jan 1 (year rollover).
	dec := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	got = monthlyQuotaCooldownSec(dec)
	jan1 := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	want = int(jan1.Sub(dec) / time.Second)
	if got != want {
		t.Errorf("monthlyQuotaCooldownSec(%v) = %d, want %d", dec, got, want)
	}
}

func TestIsMonthlyQuotaError(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		text      string
		wantQuota bool
	}{
		{"kiro 402", http.StatusPaymentRequired, "You have reached the limit.", true},
		{"codebuddy cn quota exhausted", http.StatusTooManyRequests, "额度已用尽，请访问以下链接购买加量包", true},
		{"codebuddy cn quota exhausted short", http.StatusTooManyRequests, "额度用尽", true},
		{"codex quota exhausted", http.StatusTooManyRequests, "quota exhausted", true},
		{"transient rate limit", http.StatusTooManyRequests, "rate limit reached", false},
		{"generic 429 no text", http.StatusTooManyRequests, "", false},
		{"5xx", http.StatusServiceUnavailable, "server overloaded", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMonthlyQuotaError(tc.status, tc.text); got != tc.wantQuota {
				t.Errorf("isMonthlyQuotaError(%d, %q) = %v, want %v", tc.status, tc.text, got, tc.wantQuota)
			}
		})
	}
}
