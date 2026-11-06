package resilience

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// fakeSuspensionStore records what the latch persisted so a test can assert the
// store was written, and replay it as a restart.
type fakeSuspensionStore struct {
	records  map[string]SuspensionRecord
	loadErr  error
	saveErr  error
	saves    int
	deletes  int
	loadCall int
}

func newFakeSuspensionStore() *fakeSuspensionStore {
	return &fakeSuspensionStore{records: map[string]SuspensionRecord{}}
}

func setSuspensionClock(now func() time.Time) func() {
	suspensionMu.Lock()
	previous := suspensionNow
	suspensionNow = now
	suspensionMu.Unlock()
	return func() {
		suspensionMu.Lock()
		suspensionNow = previous
		suspensionMu.Unlock()
	}
}

func (f *fakeSuspensionStore) LoadSuspensions() ([]SuspensionRecord, error) {
	f.loadCall++
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	out := make([]SuspensionRecord, 0, len(f.records))
	for _, rec := range f.records {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeSuspensionStore) SaveSuspension(rec SuspensionRecord) error {
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.records[rec.Provider] = rec
	return nil
}

func (f *fakeSuspensionStore) DeleteSuspension(provider string) error {
	f.deletes++
	delete(f.records, provider)
	return nil
}

func TestRecordAccountSuspension_LatchesOnFirstAccount(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	if IsProviderSuspended("antigravity") {
		t.Fatal("provider is suspended before any refusal")
	}

	latched := RecordAccountSuspension("antigravity", "key:aaaa1111", 403, "account suspended")
	if !latched {
		t.Fatal("RecordAccountSuspension() = false, want true: the default threshold is one account")
	}
	if !IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended() = false after a refusal, want true")
	}
	if IsProviderSuspended("opencode") {
		t.Error("an unrelated provider was halted: the latch must be per provider")
	}
}

// The whole point of the latch: a ban must not expire on a timer the way the
// breaker's own OPEN state does, because resuming the rotation spends the next
// account on the same verdict.
func TestSuspensionNeverSelfClears(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	// The latch is stamped far in the past, so any age-based expiry anyone adds
	// later reads this record as long stale and releases it. Without the
	// backdating, a wall-clock expiry would still look correct here.
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	restore := setSuspensionClock(func() time.Time { return base })
	defer restore()

	clock := base
	breaker := BreakerFor("suspension-contrast", BreakerOptions{FailureThreshold: 1, ResetTimeout: time.Second})
	breaker.Reset()
	breaker.now = func() time.Time { return clock }
	breaker.RecordFailure()

	if breaker.Allow() {
		t.Fatal("breaker admitted a request while OPEN")
	}

	RecordAccountSuspension("suspension-contrast", "key:aaaa1111", 403, "account suspended")

	clock = base.Add(1000 * time.Hour)

	if !breaker.Allow() {
		t.Error("breaker did not recover after its reset timeout: the contrast this test relies on is gone")
	}
	if !IsProviderSuspended("suspension-contrast") {
		t.Error("IsProviderSuspended() = false after time passed, want true: the latch must have no timer")
	}

	if err := ClearSuspension("suspension-contrast"); err != nil {
		t.Fatalf("ClearSuspension: %v", err)
	}
	if IsProviderSuspended("suspension-contrast") {
		t.Error("IsProviderSuspended() = true after ClearSuspension, want false")
	}
}

func TestRecordAccountSuspension_RepeatedSameAccountDoesNotAdvanceCount(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()
	SetSuspensionThreshold(2)

	for range 5 {
		if RecordAccountSuspension("antigravity", "key:aaaa1111", 403, "account suspended") {
			t.Fatal("one account reached a two-account threshold by retrying")
		}
	}
	if IsProviderSuspended("antigravity") {
		t.Fatal("IsProviderSuspended() = true with one account refused and a threshold of two")
	}

	if !RecordAccountSuspension("antigravity", "key:bbbb2222", 403, "account suspended") {
		t.Fatal("a second distinct account did not reach the threshold")
	}
	if !IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended() = false after two distinct accounts, want true")
	}

	rec, ok := SuspensionFor("antigravity")
	if !ok {
		t.Fatal("SuspensionFor() reported no record for a halted provider")
	}
	if len(rec.Accounts) != 2 {
		t.Errorf("record holds %d accounts, want 2: %v", len(rec.Accounts), rec.Accounts)
	}
}

func TestSetSuspensionThreshold_IgnoresBelowOne(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	SetSuspensionThreshold(0)
	if IsProviderSuspended("antigravity") {
		t.Fatal("a zero threshold halted a provider with no refusal at all")
	}
	SetSuspensionThreshold(-3)
	if IsProviderSuspended("antigravity") {
		t.Fatal("a negative threshold halted a provider with no refusal at all")
	}
	if !RecordAccountSuspension("antigravity", "key:aaaa1111", 403, "account suspended") {
		t.Error("threshold did not fall back to the one-account default")
	}
}

func TestSuspensionSurvivesRestart(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	store := newFakeSuspensionStore()
	if err := SetSuspensionStore(store); err != nil {
		t.Fatalf("SetSuspensionStore: %v", err)
	}
	RecordAccountSuspension("antigravity", "key:aaaa1111", 403, "account suspended")
	if store.saves == 0 {
		t.Fatal("the latch was not persisted, so a restart would resume the cascade")
	}

	ResetSuspensions()
	if IsProviderSuspended("antigravity") {
		t.Fatal("in-memory state survived the simulated process exit")
	}

	if err := SetSuspensionStore(store); err != nil {
		t.Fatalf("SetSuspensionStore after restart: %v", err)
	}
	if !IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended() = false after restart, want true: the latch must be replayed from the store")
	}

	rec, ok := SuspensionFor("antigravity")
	if !ok {
		t.Fatal("SuspensionFor() reported no record after restart")
	}
	if rec.StatusCode != 403 {
		t.Errorf("replayed status = %d, want 403", rec.StatusCode)
	}
	if rec.Reason != "account suspended" {
		t.Errorf("replayed reason = %q, want %q", rec.Reason, "account suspended")
	}
}

func TestClearSuspensionDeletesFromStore(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	store := newFakeSuspensionStore()
	if err := SetSuspensionStore(store); err != nil {
		t.Fatalf("SetSuspensionStore: %v", err)
	}
	RecordAccountSuspension("antigravity", "key:aaaa1111", 403, "account suspended")

	if err := ClearSuspension("antigravity"); err != nil {
		t.Fatalf("ClearSuspension: %v", err)
	}
	if store.deletes != 1 {
		t.Errorf("store deletes = %d, want 1", store.deletes)
	}
	if _, ok := store.records["antigravity"]; ok {
		t.Error("the record is still in the store, so a restart would halt an appealed provider again")
	}

	ResetSuspensions()
	if err := SetSuspensionStore(store); err != nil {
		t.Fatalf("SetSuspensionStore after restart: %v", err)
	}
	if IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended() = true after a cleared latch was replayed, want false")
	}
}

// A store that cannot write must not leave the provider running: losing the
// latch on restart is recoverable, spending forty accounts is not.
func TestSuspensionLatchesEvenWhenPersistenceFails(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	store := newFakeSuspensionStore()
	store.saveErr = errors.New("disk full")
	if err := SetSuspensionStore(store); err != nil {
		t.Fatalf("SetSuspensionStore: %v", err)
	}

	if !RecordAccountSuspension("antigravity", "key:aaaa1111", 403, "account suspended") {
		t.Fatal("RecordAccountSuspension() = false when the store failed, want true")
	}
	if !IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended() = false when the store failed, want true")
	}
}

func TestSetSuspensionStore_LoadErrorIsReported(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	store := newFakeSuspensionStore()
	store.loadErr = errors.New("malformed settings")
	if err := SetSuspensionStore(store); err == nil {
		t.Error("SetSuspensionStore() = nil error on a failed load, want the error surfaced")
	}
}

func TestRecordAccountSuspension_EmptyProviderIsIgnored(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	if RecordAccountSuspension("", "key:aaaa1111", 403, "account suspended") {
		t.Error("RecordAccountSuspension(\"\") = true, want false")
	}
	if IsProviderSuspended("") {
		t.Error("IsProviderSuspended(\"\") = true, want false")
	}
}

func TestAllSuspensionsIsSortedAndCopied(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	RecordAccountSuspension("opencode", "key:cccc3333", 403, "account disabled")
	RecordAccountSuspension("antigravity", "key:aaaa1111", 403, "account suspended")

	all := AllSuspensions()
	if len(all) != 2 {
		t.Fatalf("AllSuspensions() returned %d records, want 2", len(all))
	}
	if all[0].Provider != "antigravity" || all[1].Provider != "opencode" {
		t.Errorf("AllSuspensions() not sorted by provider: %q, %q", all[0].Provider, all[1].Provider)
	}

	all[0].Accounts[0] = "mutated"
	rec, ok := SuspensionFor("antigravity")
	if !ok {
		t.Fatal("SuspensionFor() reported no record")
	}
	if rec.Accounts[0] == "mutated" {
		t.Error("AllSuspensions() shares its slice with the registry: a caller can corrupt the latch")
	}
}

// AGENTS.md §4.G: the account set is keyed by connection id, so it must not
// grow without a bound. Past the bound the verdict is already decided, so
// refusing new ids costs nothing.
func TestSuspensionAccountSetIsBounded(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	for i := range suspensionAccountsMax + 500 {
		RecordAccountSuspension("antigravity", "conn-"+strconv.Itoa(i), 403, "account suspended")
	}

	rec, halted := SuspensionFor("antigravity")
	if !halted {
		t.Fatal("provider is not halted after many refusals")
	}
	if len(rec.Accounts) > suspensionAccountsMax {
		t.Errorf("account set holds %d ids, want at most %d", len(rec.Accounts), suspensionAccountsMax)
	}
	if !IsProviderSuspended("antigravity") {
		t.Error("the bound released the halt")
	}
}

// Reaching the bound must not stop the record from tracking the latest refusal:
// an operator reads the reason and the timestamp to decide what to appeal.
func TestSuspensionKeepsRecordingAfterTheBound(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	base := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	restore := setSuspensionClock(func() time.Time { return base })
	for i := range suspensionAccountsMax {
		RecordAccountSuspension("antigravity", "conn-"+strconv.Itoa(i), 403, "account suspended")
	}
	restore()

	later := base.Add(time.Hour)
	restoreLater := setSuspensionClock(func() time.Time { return later })
	defer restoreLater()

	if !RecordAccountSuspension("antigravity", "conn-overflow", 401, "account terminated") {
		t.Fatal("a refusal past the bound reported the provider as not halted")
	}

	rec, _ := SuspensionFor("antigravity")
	if rec.StatusCode != 401 {
		t.Errorf("statusCode = %d, want 401: the latest refusal must still be recorded", rec.StatusCode)
	}
	if rec.Reason != "account terminated" {
		t.Errorf("reason = %q, want the latest refusal's reason", rec.Reason)
	}
	if rec.LastSeenAt != later.Format(time.RFC3339) {
		t.Errorf("lastSeenAt = %q, want %q", rec.LastSeenAt, later.Format(time.RFC3339))
	}
}

// A threshold above one is what protects a healthy pool from a single
// false-positive suspension, so the operator's value has to reach the latch.
func TestSuspensionThresholdFromSettingsIsHonoured(t *testing.T) {
	ResetSuspensions()
	defer ResetSuspensions()

	SetSuspensionThreshold(3)

	if RecordAccountSuspension("antigravity", "conn-1", 403, "account suspended") {
		t.Error("one account reached a threshold of three")
	}
	if RecordAccountSuspension("antigravity", "conn-2", 403, "account suspended") {
		t.Error("two accounts reached a threshold of three")
	}
	if IsProviderSuspended("antigravity") {
		t.Fatal("provider halted before the threshold was met")
	}
	if !RecordAccountSuspension("antigravity", "conn-3", 403, "account suspended") {
		t.Error("the third account did not reach the threshold")
	}
	if !IsProviderSuspended("antigravity") {
		t.Error("IsProviderSuspended = false at the threshold, want true")
	}
}
