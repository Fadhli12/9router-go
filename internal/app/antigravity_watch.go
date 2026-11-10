package app

import (
	"context"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
	"9router/proxy/internal/providers"
	"9router/proxy/internal/resilience"
)

// antigravityDriftAccountKey stands in for the account a suspension normally
// names. A version drift is a fact about this build, not about any credential,
// so the key says so: SuspensionRecord.Accounts is what the dashboard shows as
// "appeal these", and putting a real connection id there would send an operator
// to appeal an account Google never refused.
const antigravityDriftAccountKey = "(no account: client version drift)"

// startAntigravityDriftWatch halts the Antigravity provider when Google ships a
// client release newer than the fingerprint this build sends.
//
// The pinned identity (providers.AntigravityCLIVersion) is what every pooled
// account presents. When it goes stale the accounts are what pay for it: the
// operator's pool was spent on exactly that event before the suspension latch
// existed. Halting on the release — rather than on the first ban — is the
// difference between losing nothing and losing the pool.
//
// It reuses the suspension latch rather than inventing a second halt: that
// latch already refuses every request with 403 provider_suspended, persists
// across restarts, and clears only through
// POST /api/resilience/suspensions/clear. An operator patches the pinned
// version, then clears it.
func startAntigravityDriftWatch(ctx context.Context, repo *db.Repo) {
	providers.StartAntigravityReleaseWatch(ctx, nil, func(d providers.AntigravityDrift, rel providers.AntigravityRelease) {
		haltAntigravityOnDrift(repo, d, rel)
	})
}

// haltAntigravityOnDrift is the watch's decision, named so it is testable
// without a 30-second boot delay and a live request to Google.
func haltAntigravityOnDrift(repo *db.Repo, d providers.AntigravityDrift, rel providers.AntigravityRelease) {
	canonical := providers.ResolveAlias("antigravity")
	if resilience.IsProviderSuspended(canonical) {
		// Already halted, for this or any other reason. Re-recording would
		// overwrite a ban verdict an operator is in the middle of appealing
		// with a version note.
		return
	}
	reason := providers.AntigravityDriftReason(d, rel)
	resilience.RecordAccountSuspension(canonical, antigravityDriftAccountKey, 0, reason)

	// The halt must be visible where the operator already looks: the provider
	// list's toggle reads isActive, so a latch that only lived in memory left
	// the provider looking enabled while every request was refused. Same
	// write the chat-path halt performs (suspension_halt.go), one bulk UPDATE.
	deactivated := int64(0)
	if repo != nil {
		var err error
		if deactivated, err = repo.DeactivateProviderConnections(canonical); err != nil {
			// The latch already refuses every request, so a failed write
			// costs visibility, not protection.
			log.Warn("antigravity_watch", "could not switch the drifted provider off in the dashboard",
				"provider", canonical, "error", err)
		}
	}

	log.Warn("antigravity_watch", "provider halted on client version drift",
		"provider", canonical, "released", d.Observed, "pinned", d.Pinned,
		"accountsDisabled", deactivated, "reason", reason)
}
