package accountpool

import "sort"

// Provider account exposure cap.
//
// Rotation on its own eventually dials every account a provider has. That is
// the right default for throughput and the wrong one for risk: when an upstream
// punishes something the accounts share — one client fingerprint, one payment
// instrument, one IP range — rotation hands it the whole pool, and every
// account needs its own appeal.
//
// The cap bounds how many accounts are reachable at any moment. The rest stay
// standby, and a standby is promoted only when an active slot is not usable, so
// the exposed set stays as small as the operator asked for.

// Candidate is one account the selector may dial.
//
// Usable carries the caller's own eligibility verdict (per-model locks, account
// cooldowns). It is an input rather than something this package computes,
// because those rules live with the selection path and must not be duplicated
// here where they could drift.
type Candidate struct {
	ID       string
	Priority int
	Usable   bool
}

// ActiveIDs returns the accounts that may be dialled, in stable order.
//
// maxActive of zero or less means unlimited, which is what an install without
// the setting reads, so the default behaviour is unchanged.
//
// Ordering is by priority then id rather than by the caller's slice order: the
// database returns rows in no guaranteed order, and an active set that reshuffles
// per request would expose the whole pool a few accounts at a time — the exact
// outcome the cap exists to prevent.
//
// Usable candidates fill the slots first and unusable ones pad whatever is left.
// Skipping the unusable is what promotes a standby when an active account is
// exhausted; padding afterwards is what keeps a fully-cooled-down pool inside
// the cap instead of returning an empty set, so the caller's cooldown handling
// still has the same candidates to reason about.
func ActiveIDs(candidates []Candidate, maxActive int) []string {
	if len(candidates) == 0 {
		return nil
	}
	ordered := make([]Candidate, len(candidates))
	copy(ordered, candidates)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Priority != ordered[j].Priority {
			return ordered[i].Priority < ordered[j].Priority
		}
		return ordered[i].ID < ordered[j].ID
	})

	if maxActive <= 0 || maxActive >= len(ordered) {
		out := make([]string, 0, len(ordered))
		for _, c := range ordered {
			out = append(out, c.ID)
		}
		return out
	}

	out := make([]string, 0, maxActive)
	for _, c := range ordered {
		if len(out) == maxActive {
			break
		}
		if c.Usable {
			out = append(out, c.ID)
		}
	}
	if len(out) == maxActive {
		return out
	}
	for _, c := range ordered {
		if len(out) == maxActive {
			break
		}
		if !c.Usable {
			out = append(out, c.ID)
		}
	}
	return out
}

// ActiveSet is ActiveIDs as a lookup, plus whether a cap applied at all.
//
// The second return value is what lets a caller tell "no cap configured" apart
// from "cap produced an empty set": treating those the same would either ignore
// the cap or drop every account.
func ActiveSet(candidates []Candidate, maxActive int) (map[string]bool, bool) {
	if maxActive <= 0 {
		return nil, false
	}
	ids := ActiveIDs(candidates, maxActive)
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, true
}
