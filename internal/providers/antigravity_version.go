package providers

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Antigravity client-version drift detection.
//
// The gateway presents a pinned Antigravity fingerprint to Google
// (AntigravityCLIVersion / AntigravityIDEVersion in client_identity.go). When
// Google ships a new Antigravity release and this gateway has not been patched
// yet, every pooled account keeps sending the stale identity — and the accounts
// are what pay for it: the operator's pool was spent on exactly that event
// before the suspension latch existed (see the ban wording pinned in
// errorclassify.go).
//
// The detectable signal is the version the real client reports. Three sources
// were considered and only one reaches a hosted gateway:
//
//   - a local `agy --version` probe: works on a workstation, but the cloud host
//     has no Antigravity install at all, so it cannot fire where the pool lives;
//   - the MITM handler's observed User-Agent: same limitation, and it is opt-in;
//   - the User-Agent on requests the Antigravity app sends to this gateway:
//     available wherever a client is actually pointed at it, cloud included.
//
// This file implements the third. It is a heuristic, not a guarantee: it sees a
// new release only once some client on this gateway has upgraded, and a ban can
// land before that happens. What it buys is that the first upgraded client ends
// the rotation instead of the fortieth account.

// antigravityUAVersion matches the version in the two User-Agent shapes the
// Antigravity clients send (antigravity_profile.go):
//
//	antigravity/cli/1.5.1 (aidev_client; os_type=…; arch=…)
//	antigravity/2.23.0
//
// The `cli/` segment is optional so both forms resolve, and the version is
// captured without the trailing metadata.
var antigravityUAVersion = regexp.MustCompile(`(?i)\bantigravity/(?:cli/)?v?(\d+(?:\.\d+)*)`)

// ParseAntigravityClientVersion returns the version an Antigravity client
// reports in its User-Agent, or "" when the header is not an Antigravity one.
func ParseAntigravityClientVersion(userAgent string) string {
	if userAgent == "" {
		return ""
	}
	m := antigravityUAVersion.FindStringSubmatch(userAgent)
	if m == nil {
		return ""
	}
	return m[1]
}

// CompareVersions orders two dotted numeric versions: -1, 0, or 1.
//
// Field-wise numeric comparison rather than string comparison, because the
// strings order wrongly exactly where it matters — "1.10.0" sorts below
// "1.9.0" lexically. A missing field counts as 0, so "1.5" == "1.5.0". A field
// that does not parse makes the comparison give up and report equal, which is
// the safe direction: an unreadable version must not be read as an upgrade.
func CompareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(strings.TrimSpace(a), "v"), ".")
	bs := strings.Split(strings.TrimPrefix(strings.TrimSpace(b), "v"), ".")
	n := max(len(as), len(bs))
	for i := 0; i < n; i++ {
		av, err := versionField(as, i)
		if err != nil {
			return 0
		}
		bv, err := versionField(bs, i)
		if err != nil {
			return 0
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionField(fields []string, i int) (int, error) {
	if i >= len(fields) {
		return 0, nil
	}
	f := strings.TrimSpace(fields[i])
	if f == "" {
		return 0, nil
	}
	return strconv.Atoi(f)
}

// AntigravityDrift describes an observed client version against the pinned one.
type AntigravityDrift struct {
	// Observed is the version a client reported.
	Observed string
	// Pinned is the identity this gateway sends upstream.
	Pinned string
	// Newer reports whether Observed is ahead of Pinned — the direction that
	// means Google shipped a release this build does not match yet.
	Newer bool
}

// antigravityObserved remembers the highest client version seen, so the halt
// decision does not depend on which client happened to call last.
//
// Two strings behind a mutex: bounded by construction, per AGENTS.md §4.G.
var (
	antigravityObservedMu  sync.RWMutex
	antigravityObservedMax string
)

// ObserveAntigravityClientVersion records a version reported by a client and
// reports the drift against the pinned identity.
//
// Only a version ahead of the pinned one sets Newer. An older client is normal
// — operators lag behind releases, and halting on that would halt the provider
// for a machine that simply has not updated. Equal is the healthy state.
func ObserveAntigravityClientVersion(userAgent string) AntigravityDrift {
	observed := ParseAntigravityClientVersion(userAgent)
	pinned := AntigravityCLIVersion
	if observed == "" {
		return AntigravityDrift{Pinned: pinned}
	}

	antigravityObservedMu.Lock()
	if antigravityObservedMax == "" || CompareVersions(observed, antigravityObservedMax) > 0 {
		antigravityObservedMax = observed
	}
	highest := antigravityObservedMax
	antigravityObservedMu.Unlock()

	return AntigravityDrift{
		Observed: highest,
		Pinned:   pinned,
		Newer:    CompareVersions(highest, pinned) > 0,
	}
}

// HighestObservedAntigravityVersion returns the highest client version seen so
// far, or "" when no Antigravity client has called.
func HighestObservedAntigravityVersion() string {
	antigravityObservedMu.RLock()
	defer antigravityObservedMu.RUnlock()
	return antigravityObservedMax
}

// ResetObservedAntigravityVersion clears the remembered version. For tests, and
// for an operator who patched the pinned identity and wants a clean slate.
func ResetObservedAntigravityVersion() {
	antigravityObservedMu.Lock()
	antigravityObservedMax = ""
	antigravityObservedMu.Unlock()
}
