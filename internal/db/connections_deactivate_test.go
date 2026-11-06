package db

import (
	json "encoding/json/v2"
	"strconv"
	"testing"
)

func seedDeactivateConns(t *testing.T, repo *Repo, provider string, n int, active int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		id := provider + "-" + strconv.Itoa(i)
		data, _ := json.Marshal(map[string]any{"apiKey": "sk-" + id})
		if _, err := repo.RawDB().Exec(
			`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
			VALUES (?, ?, 'apikey', 'Seed', ?, ?, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			id, provider, i, active, string(data)); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
}

func activeCount(t *testing.T, repo *Repo, provider string) int {
	t.Helper()
	var n int
	if err := repo.RawDB().QueryRow(
		`SELECT COUNT(*) FROM providerConnections WHERE provider = ? AND isActive = 1`, provider).Scan(&n); err != nil {
		t.Fatalf("count active for %s: %v", provider, err)
	}
	return n
}

// The dashboard's per-provider toggle reads isActive, so a halt that does not
// flip it leaves the UI showing a provider as on while every request is
// refused. This is the write that makes the halt visible where the operator
// already looks.
func TestDeactivateProviderConnectionsFlipsTheWholeProvider(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepo(database)

	seedDeactivateConns(t, repo, "antigravity", 40, 1)
	seedDeactivateConns(t, repo, "kiro", 5, 1)

	affected, err := repo.DeactivateProviderConnections("antigravity")
	if err != nil {
		t.Fatalf("DeactivateProviderConnections: %v", err)
	}
	if affected != 40 {
		t.Errorf("affected = %d, want 40", affected)
	}
	if got := activeCount(t, repo, "antigravity"); got != 0 {
		t.Errorf("antigravity still has %d active connections, want 0", got)
	}
	if got := activeCount(t, repo, "kiro"); got != 5 {
		t.Errorf("kiro has %d active connections, want 5: the halt must not touch another provider", got)
	}
}

// Called twice the second call must report zero rather than re-counting rows it
// did not change, so a caller can tell a fresh halt from a repeat.
func TestDeactivateProviderConnectionsIsIdempotent(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepo(database)

	seedDeactivateConns(t, repo, "antigravity", 3, 1)

	first, err := repo.DeactivateProviderConnections("antigravity")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first != 3 {
		t.Fatalf("first call affected = %d, want 3", first)
	}

	second, err := repo.DeactivateProviderConnections("antigravity")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second != 0 {
		t.Errorf("second call affected = %d, want 0", second)
	}
	if got := activeCount(t, repo, "antigravity"); got != 0 {
		t.Errorf("antigravity has %d active connections, want 0", got)
	}
}

// A provider whose accounts are already off must not report a halt it did not
// perform, and an unknown provider must be a no-op rather than an error.
func TestDeactivateProviderConnectionsEdgeCases(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepo(database)

	seedDeactivateConns(t, repo, "antigravity", 2, 0)

	affected, err := repo.DeactivateProviderConnections("antigravity")
	if err != nil {
		t.Fatalf("already-off provider: %v", err)
	}
	if affected != 0 {
		t.Errorf("already-off provider affected = %d, want 0", affected)
	}

	if affected, err = repo.DeactivateProviderConnections("does-not-exist"); err != nil || affected != 0 {
		t.Errorf("unknown provider = (%d, %v), want (0, nil)", affected, err)
	}
	if affected, err = repo.DeactivateProviderConnections(""); err != nil || affected != 0 {
		t.Errorf("empty provider = (%d, %v), want (0, nil)", affected, err)
	}

	var nilRepo *Repo
	if affected, err = nilRepo.DeactivateProviderConnections("antigravity"); err != nil || affected != 0 {
		t.Errorf("nil repo = (%d, %v), want (0, nil)", affected, err)
	}
}
