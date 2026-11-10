package app

import (
	"path/filepath"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/dbtest"
	"9router/proxy/internal/providers"
	"9router/proxy/internal/resilience"
)

// newDriftTestRepo opens a real database with the production schema, so the
// deactivate write is exercised against the statement that will run in
// production rather than a mock of it.
func newDriftTestRepo(t *testing.T) *db.Repo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "drift.sqlite")
	database, err := db.OpenDatabase(path)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := dbtest.CreateTables(database); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	return db.NewRepo(database)
}

// The halt must reach the dashboard toggle, not only the routing layer: the
// provider list reads isActive, so a halt that leaves it alone shows the
// provider as enabled while every request is refused — the exact gap this
// write closes.
func TestHaltAntigravityOnDriftDeactivatesConnections(t *testing.T) {
	resilience.ResetSuspensions()
	t.Cleanup(resilience.ResetSuspensions)

	repo := newDriftTestRepo(t)
	for i, name := range []string{"acct-a", "acct-b"} {
		if err := repo.CreateProviderConnection(name, "antigravity", "oauth", name, "key-"+name); err != nil {
			t.Fatalf("CreateProviderConnection %s: %v", name, err)
		}
		_ = i
	}
	for _, name := range []string{"acct-a", "acct-b"} {
		if err := repo.SetConnectionStatus(name, true); err != nil {
			t.Fatalf("SetConnectionStatus %s: %v", name, err)
		}
	}

	haltAntigravityOnDrift(repo,
		providers.AntigravityDrift{Observed: "1.5.4", Pinned: "1.5.3", Newer: true},
		providers.AntigravityRelease{Version: "1.5.4", RolloutPercent: 100},
	)

	conns, err := repo.GetProviderConnections("antigravity", false)
	if err != nil {
		t.Fatalf("GetProviderConnections: %v", err)
	}
	if len(conns) != 2 {
		t.Fatalf("got %d connections, want 2", len(conns))
	}
	for _, c := range conns {
		if c.IsActive != 0 {
			t.Errorf("connection %s still isActive=%d; the provider list would show it enabled", c.ID, c.IsActive)
		}
	}
}

// A nil repo is the no-database degenerate case: the latch alone still stops
// the rotation, so the halt decision must not crash without one.
func TestHaltAntigravityOnDriftNilRepo(t *testing.T) {
	resilience.ResetSuspensions()
	t.Cleanup(resilience.ResetSuspensions)

	haltAntigravityOnDrift(nil,
		providers.AntigravityDrift{Observed: "1.5.4", Pinned: "1.5.3", Newer: true},
		providers.AntigravityRelease{Version: "1.5.4", RolloutPercent: 100},
	)

	if !resilience.IsProviderSuspended(providers.ResolveAlias("antigravity")) {
		t.Fatal("provider not halted with a nil repo")
	}
}
