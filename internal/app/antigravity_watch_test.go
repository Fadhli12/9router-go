package app

import (
	"strings"
	"testing"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/resilience"
)

// The halt is the whole point of the watch: a client release newer than this
// build's pinned fingerprint must stop the rotation before the accounts pay for
// it. These tests drive the decision directly, because reaching it through
// StartAntigravityReleaseWatch would need a 30-second boot delay and a live
// request to Google.
func TestHaltAntigravityOnDrift(t *testing.T) {
	canonical := providers.ResolveAlias("antigravity")

	t.Run("drift halts the provider and names both versions", func(t *testing.T) {
		resilience.ResetSuspensions()
		t.Cleanup(resilience.ResetSuspensions)

		haltAntigravityOnDrift(
			providers.AntigravityDrift{Observed: "1.5.3", Pinned: "1.5.1", Newer: true},
			providers.AntigravityRelease{Version: "1.5.3", RolloutPercent: 100},
		)

		if !resilience.IsProviderSuspended(canonical) {
			t.Fatal("provider not halted; the rotation would spend every account on a stale fingerprint")
		}
		rec, ok := resilience.SuspensionFor(canonical)
		if !ok {
			t.Fatal("no suspension record")
		}
		for _, want := range []string{"1.5.3", "1.5.1"} {
			if !strings.Contains(rec.Reason, want) {
				t.Errorf("reason %q does not name version %q", rec.Reason, want)
			}
		}
		// The account list is what the dashboard shows as "appeal these". A
		// version drift refused no credential, so it must not name one.
		if len(rec.Accounts) != 1 || rec.Accounts[0] != antigravityDriftAccountKey {
			t.Errorf("Accounts = %v, want the no-account sentinel", rec.Accounts)
		}
	})

	t.Run("an existing halt is not overwritten", func(t *testing.T) {
		resilience.ResetSuspensions()
		t.Cleanup(resilience.ResetSuspensions)

		// A real ban verdict an operator is mid-appeal on.
		const banReason = "This service has been disabled in this account for violation of Terms of Service."
		resilience.RecordAccountSuspension(canonical, "conn-42", 403, banReason)

		haltAntigravityOnDrift(
			providers.AntigravityDrift{Observed: "9.9.9", Pinned: "1.5.1", Newer: true},
			providers.AntigravityRelease{Version: "9.9.9", RolloutPercent: 100},
		)

		rec, ok := resilience.SuspensionFor(canonical)
		if !ok {
			t.Fatal("no suspension record")
		}
		if rec.Reason != banReason {
			t.Errorf("the ban verdict was overwritten with a version note:\n got  %q\n want %q", rec.Reason, banReason)
		}
		if len(rec.Accounts) != 1 || rec.Accounts[0] != "conn-42" {
			t.Errorf("Accounts = %v, want the refused account to survive", rec.Accounts)
		}
	})
}
