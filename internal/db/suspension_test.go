package db

import (
	"testing"

	"9router/proxy/internal/resilience"
)

func newSuspensionTestStore(t *testing.T) (*SuspensionStore, *Repo) {
	t.Helper()
	database, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	repo := NewRepo(database)
	return NewSuspensionStore(repo), repo
}

func TestSuspensionStoreRoundTrip(t *testing.T) {
	store, _ := newSuspensionTestStore(t)

	records, err := store.LoadSuspensions()
	if err != nil {
		t.Fatalf("LoadSuspensions on a fresh database: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("fresh database reported %d latches, want 0", len(records))
	}

	want := resilience.SuspensionRecord{
		Provider:    "antigravity",
		StatusCode:  403,
		Reason:      "account suspended",
		Accounts:    []string{"conn-1"},
		FirstSeenAt: "2026-11-06T00:00:00Z",
		LastSeenAt:  "2026-11-06T00:00:00Z",
	}
	if err := store.SaveSuspension(want); err != nil {
		t.Fatalf("SaveSuspension: %v", err)
	}

	records, err = store.LoadSuspensions()
	if err != nil {
		t.Fatalf("LoadSuspensions: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("loaded %d latches, want 1", len(records))
	}
	got := records[0]
	if got.Provider != want.Provider {
		t.Errorf("provider = %q, want %q", got.Provider, want.Provider)
	}
	if got.StatusCode != want.StatusCode {
		t.Errorf("status = %d, want %d", got.StatusCode, want.StatusCode)
	}
	if got.Reason != want.Reason {
		t.Errorf("reason = %q, want %q", got.Reason, want.Reason)
	}
	if len(got.Accounts) != 1 || got.Accounts[0] != "conn-1" {
		t.Errorf("accounts = %v, want [conn-1]", got.Accounts)
	}
}

// Halting a second provider must not drop the first: the settings row holds
// every latch, so a blind write would release a provider nobody appealed.
func TestSuspensionStorePreservesOtherProviders(t *testing.T) {
	store, _ := newSuspensionTestStore(t)

	for _, provider := range []string{"antigravity", "opencode"} {
		if err := store.SaveSuspension(resilience.SuspensionRecord{
			Provider: provider, StatusCode: 403, Reason: "account suspended",
			Accounts: []string{"conn-" + provider},
		}); err != nil {
			t.Fatalf("SaveSuspension(%s): %v", provider, err)
		}
	}

	records, err := store.LoadSuspensions()
	if err != nil {
		t.Fatalf("LoadSuspensions: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("loaded %d latches, want 2: %+v", len(records), records)
	}

	if err := store.DeleteSuspension("antigravity"); err != nil {
		t.Fatalf("DeleteSuspension: %v", err)
	}
	records, err = store.LoadSuspensions()
	if err != nil {
		t.Fatalf("LoadSuspensions after delete: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("loaded %d latches after deleting one, want 1", len(records))
	}
	if records[0].Provider != "opencode" {
		t.Errorf("surviving latch = %q, want opencode", records[0].Provider)
	}
}

// The latch lives alongside the operator's other settings, so writing it must
// not clobber them and deleting the last latch must not delete the row.
func TestSuspensionStoreLeavesUnrelatedSettingsIntact(t *testing.T) {
	store, repo := newSuspensionTestStore(t)

	if err := repo.UpdateSettingsRaw(map[string]any{"autoUpdate": true, "rtkEnabled": false}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	if err := store.SaveSuspension(resilience.SuspensionRecord{
		Provider: "antigravity", StatusCode: 403, Reason: "account suspended",
		Accounts: []string{"conn-1"},
	}); err != nil {
		t.Fatalf("SaveSuspension: %v", err)
	}

	raw, err := repo.GetSettingsRaw()
	if err != nil {
		t.Fatalf("GetSettingsRaw: %v", err)
	}
	if raw["autoUpdate"] != true {
		t.Errorf("autoUpdate = %v, want true: the latch write clobbered an unrelated setting", raw["autoUpdate"])
	}
	if raw["rtkEnabled"] != false {
		t.Errorf("rtkEnabled = %v, want false", raw["rtkEnabled"])
	}

	if err := store.DeleteSuspension("antigravity"); err != nil {
		t.Fatalf("DeleteSuspension: %v", err)
	}
	raw, err = repo.GetSettingsRaw()
	if err != nil {
		t.Fatalf("GetSettingsRaw after delete: %v", err)
	}
	if raw["autoUpdate"] != true {
		t.Errorf("autoUpdate = %v after delete, want true", raw["autoUpdate"])
	}
	if _, present := raw[settingsSuspensionsKey]; present {
		t.Error("the empty latch map was left behind in settings")
	}
}

// This is the restart contract the whole latch depends on: the store is what
// turns an in-memory halt into one that survives a reboot.
func TestSuspensionStoreReplaysIntoResilience(t *testing.T) {
	store, _ := newSuspensionTestStore(t)
	resilience.ResetSuspensions()
	defer resilience.ResetSuspensions()

	if err := resilience.SetSuspensionStore(store); err != nil {
		t.Fatalf("SetSuspensionStore: %v", err)
	}
	resilience.RecordAccountSuspension("antigravity", "conn-1", 403, "account suspended")
	if !resilience.IsProviderSuspended("antigravity") {
		t.Fatal("provider was not halted in memory")
	}

	resilience.ResetSuspensions()
	if resilience.IsProviderSuspended("antigravity") {
		t.Fatal("in-memory state survived the simulated restart")
	}

	if err := resilience.SetSuspensionStore(store); err != nil {
		t.Fatalf("SetSuspensionStore after restart: %v", err)
	}
	if !resilience.IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended = false after restart, want true: the halt must be replayed from sqlite")
	}
}

func TestSuspensionStoreIgnoresMalformedStoredValue(t *testing.T) {
	store, repo := newSuspensionTestStore(t)

	if err := repo.UpdateSettingsRaw(map[string]any{settingsSuspensionsKey: "not-an-object"}); err != nil {
		t.Fatalf("seed malformed value: %v", err)
	}

	if _, err := store.LoadSuspensions(); err == nil {
		t.Log("LoadSuspensions tolerated a malformed value")
	}

	if err := store.SaveSuspension(resilience.SuspensionRecord{
		Provider: "antigravity", StatusCode: 403, Reason: "account suspended",
		Accounts: []string{"conn-1"},
	}); err != nil {
		t.Fatalf("SaveSuspension over a malformed value: %v", err)
	}
	records, err := store.LoadSuspensions()
	if err != nil {
		t.Fatalf("LoadSuspensions after repair: %v", err)
	}
	if len(records) != 1 || records[0].Provider != "antigravity" {
		t.Errorf("loaded %+v, want one antigravity latch: a corrupt value must not stop new halts persisting", records)
	}
}

func TestSuspensionStoreNilSafety(t *testing.T) {
	var store *SuspensionStore
	if records, err := store.LoadSuspensions(); err != nil || records != nil {
		t.Errorf("nil store LoadSuspensions = (%v, %v), want (nil, nil)", records, err)
	}
	if err := store.SaveSuspension(resilience.SuspensionRecord{Provider: "x"}); err != nil {
		t.Errorf("nil store SaveSuspension = %v, want nil", err)
	}
	if err := store.DeleteSuspension("x"); err != nil {
		t.Errorf("nil store DeleteSuspension = %v, want nil", err)
	}
}
