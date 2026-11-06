package resilience

import (
	"sort"
	"sync"
	"time"
)

// Provider-level suspension latch.
//
// The circuit breaker answers "is this provider failing?" and recovers on a
// timer. That is right for an outage and wrong for a ban: when an upstream
// suspends an account, the cause is usually something the whole pool shares — a
// pinned client fingerprint, one payment instrument, one IP range — so the
// breaker's own recovery would resume the rotation and spend the next account
// on the same verdict. Forty accounts can be lost that way, each needing its
// own appeal.
//
// The latch therefore has no timer at all. Once set it stays set until an
// operator clears it, and it is persisted so a restart cannot resume the
// cascade.
type SuspensionRecord struct {
	Provider    string   `json:"provider"`
	StatusCode  int      `json:"statusCode"`
	Reason      string   `json:"reason"`
	Accounts    []string `json:"accounts"`
	FirstSeenAt string   `json:"firstSeenAt"`
	LastSeenAt  string   `json:"lastSeenAt"`
}

// SuspensionStore persists latches across restarts.
//
// An interface rather than a direct database call so this package keeps its
// stdlib-only dependency set: the wiring layer injects a store backed by the
// settings row. A nil store degrades to in-memory only, which still stops the
// cascade within the running process.
type SuspensionStore interface {
	LoadSuspensions() ([]SuspensionRecord, error)
	SaveSuspension(rec SuspensionRecord) error
	DeleteSuspension(provider string) error
}

// defaultSuspensionThreshold is how many distinct accounts must be refused
// before the provider is halted. One, because the operator's requirement is
// that a single suspended account stops the provider: the second account is
// already a second appeal.
const defaultSuspensionThreshold = 1

// suspensionAccountsMax bounds the remembered account ids per provider. Large
// enough that the list stays complete for any realistic pool, so the operator
// still sees every account to appeal.
const suspensionAccountsMax = 256

var (
	suspensionMu        sync.RWMutex
	suspensions         = map[string]*SuspensionRecord{}
	suspensionSeen      = map[string]map[string]struct{}{}
	suspensionStore     SuspensionStore
	suspensionThreshold = defaultSuspensionThreshold
	// suspensionNow is the latch's only clock, so a test can advance time and
	// prove the latch does not expire. Without the seam, a timer-based
	// expiry could be added here and no test could observe it.
	suspensionNow = time.Now
)

// SetSuspensionStore installs the persistence backend and replays whatever it
// holds, so a gateway that restarts while a provider is halted comes back
// halted. Called once during startup.
func SetSuspensionStore(store SuspensionStore) error {
	suspensionMu.Lock()
	suspensionStore = store
	suspensionMu.Unlock()

	if store == nil {
		return nil
	}
	records, err := store.LoadSuspensions()
	if err != nil {
		return err
	}
	suspensionMu.Lock()
	defer suspensionMu.Unlock()
	for _, rec := range records {
		if rec.Provider == "" {
			continue
		}
		stored := rec
		suspensions[rec.Provider] = &stored
		seen := make(map[string]struct{}, len(rec.Accounts))
		for _, acct := range rec.Accounts {
			seen[acct] = struct{}{}
		}
		suspensionSeen[rec.Provider] = seen
	}
	return nil
}

// SetSuspensionThreshold overrides how many distinct accounts must be refused
// before the latch closes. Values below 1 are ignored: a threshold of zero
// would halt a provider no account had been refused on.
func SetSuspensionThreshold(n int) {
	if n < 1 {
		return
	}
	suspensionMu.Lock()
	suspensionThreshold = n
	suspensionMu.Unlock()
}

// RecordAccountSuspension folds one refusal into the provider's latch and
// reports whether the provider is now halted.
//
// accountKey distinguishes the accounts seen so far; an empty key still counts
// as one account. Repeated refusals from the same account do not advance the
// count, so a client retrying against one suspended account cannot reach a
// threshold meant to describe separate accounts.
func RecordAccountSuspension(provider, accountKey string, statusCode int, reason string) bool {
	if provider == "" {
		return false
	}
	now := suspensionNow().UTC().Format(time.RFC3339)

	suspensionMu.Lock()
	seen, ok := suspensionSeen[provider]
	if !ok {
		seen = map[string]struct{}{}
		suspensionSeen[provider] = seen
	}
	// Bounded per AGENTS.md §4.G: the key is a connection id, so an unbounded
	// map keyed by it grows with every refusal the gateway ever sees. Past the
	// bound the set stops accepting new ids, which costs nothing that matters —
	// the threshold has long been crossed, so another id cannot change the
	// verdict, only lengthen the list shown to the operator.
	if _, known := seen[accountKey]; known || len(seen) < suspensionAccountsMax {
		seen[accountKey] = struct{}{}
	}

	rec, existed := suspensions[provider]
	if !existed {
		rec = &SuspensionRecord{Provider: provider, FirstSeenAt: now}
		suspensions[provider] = rec
	}
	rec.StatusCode = statusCode
	rec.Reason = reason
	rec.LastSeenAt = now
	rec.Accounts = sortedKeys(seen)

	latched := len(seen) >= suspensionThreshold
	snapshot := *rec
	snapshot.Accounts = append([]string(nil), rec.Accounts...)
	store := suspensionStore
	suspensionMu.Unlock()

	if latched && store != nil {
		// A persistence failure must not un-halt the provider: the latch is
		// already set in memory, and losing it on restart is the lesser harm
		// compared with refusing to halt at all.
		_ = store.SaveSuspension(snapshot)
	}
	return latched
}

// IsProviderSuspended reports whether the provider is halted.
func IsProviderSuspended(provider string) bool {
	if provider == "" {
		return false
	}
	suspensionMu.RLock()
	defer suspensionMu.RUnlock()
	rec, ok := suspensions[provider]
	if !ok {
		return false
	}
	return len(rec.Accounts) >= suspensionThreshold
}

// SuspensionFor returns the provider's latch record, or false when it is not
// halted.
func SuspensionFor(provider string) (SuspensionRecord, bool) {
	suspensionMu.RLock()
	defer suspensionMu.RUnlock()
	rec, ok := suspensions[provider]
	if !ok || len(rec.Accounts) < suspensionThreshold {
		return SuspensionRecord{}, false
	}
	out := *rec
	out.Accounts = append([]string(nil), rec.Accounts...)
	return out, true
}

// AllSuspensions returns every latch, for the dashboard.
func AllSuspensions() []SuspensionRecord {
	suspensionMu.RLock()
	defer suspensionMu.RUnlock()
	out := make([]SuspensionRecord, 0, len(suspensions))
	for _, rec := range suspensions {
		if len(rec.Accounts) < suspensionThreshold {
			continue
		}
		copied := *rec
		copied.Accounts = append([]string(nil), rec.Accounts...)
		out = append(out, copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// ClearSuspension releases the latch. This is the only way a halted provider
// resumes: the operator appealed, so the gateway may try again.
func ClearSuspension(provider string) error {
	if provider == "" {
		return nil
	}
	suspensionMu.Lock()
	delete(suspensions, provider)
	delete(suspensionSeen, provider)
	store := suspensionStore
	suspensionMu.Unlock()

	if store == nil {
		return nil
	}
	return store.DeleteSuspension(provider)
}

// ResetSuspensions clears every latch in memory without touching the store.
// For tests; an operator clears one provider at a time so an unrelated halt is
// never released by accident.
func ResetSuspensions() {
	suspensionMu.Lock()
	defer suspensionMu.Unlock()
	suspensions = map[string]*SuspensionRecord{}
	suspensionSeen = map[string]map[string]struct{}{}
	suspensionThreshold = defaultSuspensionThreshold
	suspensionStore = nil
	suspensionNow = time.Now
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
