package accountpool

import (
	"strconv"
	"testing"
)

func pool(n int, usable func(i int) bool) []Candidate {
	out := make([]Candidate, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, Candidate{
			ID:       "conn-" + strconv.Itoa(i),
			Priority: i,
			Usable:   usable(i),
		})
	}
	return out
}

func allUsable(int) bool { return true }

func TestActiveIDsCapsThePool(t *testing.T) {
	tests := []struct {
		name      string
		accounts  int
		maxActive int
		want      int
	}{
		{"cap below pool size", 40, 10, 10},
		{"cap of one", 40, 1, 1},
		{"cap equal to pool size", 10, 10, 10},
		{"cap above pool size", 3, 10, 3},
		{"zero means unlimited", 40, 0, 40},
		{"negative means unlimited", 40, -5, 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ActiveIDs(pool(tt.accounts, allUsable), tt.maxActive)
			if len(got) != tt.want {
				t.Errorf("ActiveIDs returned %d accounts, want %d", len(got), tt.want)
			}
		})
	}
}

// An active set that reshuffles per request would expose the whole pool a few
// accounts at a time, which is the outcome the cap exists to prevent.
func TestActiveIDsIsStableAcrossCalls(t *testing.T) {
	candidates := pool(40, allUsable)

	first := ActiveIDs(candidates, 10)
	for range 20 {
		got := ActiveIDs(candidates, 10)
		if len(got) != len(first) {
			t.Fatalf("set size changed between calls: %d then %d", len(first), len(got))
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("set changed between calls at %d: %q then %q", i, first[i], got[i])
			}
		}
	}
}

// The database returns rows in no guaranteed order, so the cap must not depend
// on the caller's slice order.
func TestActiveIDsIgnoresInputOrder(t *testing.T) {
	ascending := pool(40, allUsable)

	reversed := make([]Candidate, len(ascending))
	for i, c := range ascending {
		reversed[len(ascending)-1-i] = c
	}

	want := ActiveIDs(ascending, 10)
	got := ActiveIDs(reversed, 10)
	if len(got) != len(want) {
		t.Fatalf("reversed input produced %d accounts, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reversed input changed the set at %d: %q, want %q", i, got[i], want[i])
		}
	}
}

// This is the auto-toggle the operator asked for: an exhausted active account
// must hand its slot to a standby rather than shrinking the active set.
func TestExhaustedActiveAccountPromotesExactlyOneStandby(t *testing.T) {
	const cap10 = 10

	healthy := ActiveIDs(pool(40, allUsable), cap10)
	if len(healthy) != cap10 {
		t.Fatalf("baseline set has %d accounts, want %d", len(healthy), cap10)
	}

	// conn-3 is exhausted; everything else is fine.
	withOneDown := ActiveIDs(pool(40, func(i int) bool { return i != 3 }), cap10)
	if len(withOneDown) != cap10 {
		t.Fatalf("set shrank to %d after one account was exhausted, want %d", len(withOneDown), cap10)
	}

	for _, id := range withOneDown {
		if id == "conn-3" {
			t.Error("the exhausted account is still in the active set")
		}
	}

	baseline := map[string]bool{}
	for _, id := range healthy {
		baseline[id] = true
	}
	promoted := make([]string, 0, 1)
	for _, id := range withOneDown {
		if !baseline[id] {
			promoted = append(promoted, id)
		}
	}
	if len(promoted) != 1 {
		t.Errorf("promoted %v, want exactly one standby", promoted)
	}
	if len(promoted) == 1 && promoted[0] != "conn-11" {
		t.Errorf("promoted %q, want conn-11: promotion must follow the deterministic order", promoted[0])
	}
}

// A pool where everything is cooling down must still yield candidates inside
// the cap, so the caller's existing cooldown handling has the same accounts to
// reason about instead of seeing an empty provider.
func TestFullyUnusablePoolStillFillsTheCap(t *testing.T) {
	got := ActiveIDs(pool(40, func(int) bool { return false }), 10)
	if len(got) != 10 {
		t.Fatalf("returned %d accounts for a fully cooled-down pool, want 10", len(got))
	}
}

func TestActiveIDsEmptyPool(t *testing.T) {
	if got := ActiveIDs(nil, 10); got != nil {
		t.Errorf("ActiveIDs(nil) = %v, want nil", got)
	}
	if got := ActiveIDs([]Candidate{}, 10); got != nil {
		t.Errorf("ActiveIDs(empty) = %v, want nil", got)
	}
}

// Ties on priority must break on id, or two accounts the operator gave the same
// priority would make the set unstable.
func TestActiveIDsBreaksPriorityTiesOnID(t *testing.T) {
	candidates := []Candidate{
		{ID: "conn-b", Priority: 1, Usable: true},
		{ID: "conn-a", Priority: 1, Usable: true},
		{ID: "conn-c", Priority: 1, Usable: true},
	}
	got := ActiveIDs(candidates, 2)
	if len(got) != 2 || got[0] != "conn-a" || got[1] != "conn-b" {
		t.Errorf("ActiveIDs = %v, want [conn-a conn-b]", got)
	}
}

func TestActiveSetReportsWhetherACapApplied(t *testing.T) {
	candidates := pool(40, allUsable)

	if set, capped := ActiveSet(candidates, 0); capped || set != nil {
		t.Errorf("ActiveSet(_, 0) = (%v, %v), want (nil, false): no cap configured", set, capped)
	}
	if set, capped := ActiveSet(candidates, -1); capped || set != nil {
		t.Errorf("ActiveSet(_, -1) = (%v, %v), want (nil, false)", set, capped)
	}

	set, capped := ActiveSet(candidates, 10)
	if !capped {
		t.Fatal("ActiveSet(_, 10) reported no cap applied")
	}
	if len(set) != 10 {
		t.Errorf("ActiveSet returned %d accounts, want 10", len(set))
	}
	if !set["conn-1"] {
		t.Error("conn-1 is not in the active set but should be the first promoted")
	}
	if set["conn-40"] {
		t.Error("conn-40 is in the active set: a standby must never be dialled")
	}
}
