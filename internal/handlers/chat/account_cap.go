package chat

import (
	"9router/proxy/internal/accountpool"
	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
	"9router/proxy/internal/models"
	"time"
)

// capConnectionsToActivePool drops the provider's standby accounts from the
// candidate list, leaving at most maxActive reachable.
//
// Usability here is the account's PERSISTENT state — a per-model lock or an
// account cooldown — and deliberately not the request-scoped exclude set. The
// difference decides how far one request can reach:
//
//   - persistent state promotes across requests, which is the auto-toggle an
//     operator wants: an exhausted account is cooled down, so the next request
//     hands its slot to a standby;
//   - the exclude set grows inside one request as accounts fail, so promoting
//     on it would let a single request walk the whole pool — the exposure the
//     cap exists to bound. That request fails instead, and the cooldowns it
//     wrote promote standbys on the next one.
//
// A probe is left uncapped: it is the operator checking one specific account,
// not traffic that risks the pool.
func capConnectionsToActivePool(
	h *ChatHandler,
	connections []*models.ProviderConnection,
	provider, model string,
	settings *db.SettingsData,
	isProbe bool,
) []*models.ProviderConnection {
	if isProbe || len(connections) <= 1 {
		return connections
	}
	maxActive := maxActiveAccountsFor(settings, provider)
	if maxActive <= 0 || len(connections) <= maxActive {
		return connections
	}

	now := time.Now()
	candidates := make([]accountpool.Candidate, 0, len(connections))
	for _, c := range connections {
		candidates = append(candidates, accountpool.Candidate{
			ID:       c.ID,
			Priority: connectionPriority(c),
			Usable:   !connectionUnavailableNow(c, provider, model, now),
		})
	}

	active, capped := accountpool.ActiveSet(candidates, maxActive)
	if !capped {
		return connections
	}

	out := make([]*models.ProviderConnection, 0, len(active))
	for _, c := range connections {
		if active[c.ID] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return connections
	}
	log.Debug("connections", "account pool capped",
		"provider", provider, "model", model,
		"active", len(out), "standby", len(connections)-len(out), "cap", maxActive)
	return out
}

// maxActiveAccountsFor reads the provider's cap. Zero means unlimited, which is
// what an install without the setting reads.
func maxActiveAccountsFor(settings *db.SettingsData, provider string) int {
	if settings == nil || settings.ProviderStrategies == nil {
		return 0
	}
	if strat, ok := settings.ProviderStrategies[provider]; ok {
		return strat.MaxActiveAccounts
	}
	return 0
}

// connectionPriority orders the pool. A row without an explicit priority sorts
// last so an operator's assignment always wins, and ties break on id inside the
// resolver.
func connectionPriority(c *models.ProviderConnection) int {
	if c == nil || c.Priority == nil {
		return 1 << 30
	}
	return *c.Priority
}

// connectionUnavailableNow reports whether the account cannot serve this model
// right now, applying the same checks as the selection loop: the per-model
// lock, the provider quota cache, and the account-scoped cooldown.
//
// Every check reads the row's own JSON or process memory, never the database.
// The equivalent helper the selection loop uses (connectionModelBlocked) issues
// one SELECT per account — fine for the single row it reaches, but this runs
// over the whole page, so routing a 40-account pool would cost 40 statements
// (80 when the canonical lock key differs from the requested model) for data
// already in hand. AGENTS.md §4.A forbids that, and the 1 vCPU target in §4.G
// is why.
func connectionUnavailableNow(
	c *models.ProviderConnection,
	provider, model string,
	now time.Time,
) bool {
	if model != "" {
		lockKey := canonicalLockModel(provider, model)
		if db.ConnectionModelLockedFrom(c.Data, lockKey) {
			return true
		}
		if lockKey != model && db.ConnectionModelLockedFrom(c.Data, model) {
			return true
		}
		if quotaCacheBlocked(provider, c.ID, model) {
			return true
		}
	}
	until, ok := db.ConnectionBlockedUntil(c.Data)
	return ok && until.After(now)
}
