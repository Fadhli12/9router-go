package dashboard

import (
	"9router/proxy/internal/log"
	"9router/proxy/internal/models"
	"9router/proxy/internal/providers"
	"9router/proxy/internal/resilience"
)

// releaseProviderHaltOnEnable clears a provider's suspension latch when an
// operator switches one of its connections back on.
//
// This is what makes the dashboard toggle the manual release the halt requires.
// Without it the toggle is a no-op: a suspension switches every connection off,
// the operator turns one back on after appealing, and the resolver still
// refuses every request because the latch is independent of isActive. The
// provider would look enabled and stay dead.
//
// Only an enable clears it. A disable must leave the latch alone — an operator
// turning accounts off is not asserting the appeal succeeded.
//
// A drift halt is exempt: its exit is patching the pinned identity, not
// appealing an account, so no toggle may release it. The connection enable
// itself is refused upstream of this call (handleUpdateConnection); this guard
// keeps the latch safe if any other path re-enables a connection.
func releaseProviderHaltOnEnable(conn *models.ProviderConnection, enabled bool) {
	if !enabled || conn == nil || conn.Provider == "" {
		return
	}
	canonical := providers.ResolveAlias(conn.Provider)
	rec, halted := resilience.SuspensionFor(canonical)
	if !halted {
		return
	}
	if resilience.IsDriftHalt(rec) {
		return
	}
	if err := resilience.ClearSuspension(canonical); err != nil {
		log.Warn("resilience", "could not clear the provider halt on re-enable",
			"provider", canonical, "conn", conn.ID, "error", err)
		return
	}
	log.Warn("resilience", "provider halt cleared by re-enabling an account from the dashboard",
		"provider", canonical, "conn", conn.ID)
}
