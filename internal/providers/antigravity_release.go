package providers

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"9router/proxy/internal/log"
)

// Antigravity release polling.
//
// The gateway presents a pinned Antigravity fingerprint upstream
// (AntigravityCLIVersion). When Google ships a release this build does not
// match, the pooled accounts pay for it — the operator's pool was spent on
// exactly that event before the suspension latch existed.
//
// The signal is the CLI's own auto-updater service, found embedded in the agy
// binary. It answers an unauthenticated GET with free text:
//
//	Antigravity CLI auto updater is running! Stable Version: 1.5.3. Rolled out to 100%
//
// This is the one source that works on a hosted gateway. The alternatives all
// need the real client on the same host: a local `agy --version` probe, the
// MITM handler's observed User-Agent, and the User-Agent on inbound requests
// are all desktop-only — the cloud host has no Antigravity install, and its
// accounts are driven headlessly by the gateway itself.
const AntigravityUpdaterURL = "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app"

// AntigravityReleasePollInterval is how often the updater service is asked.
//
// Hourly: a release is a human-scale event, and the cost of noticing it an hour
// late is one hour of exposure, against a request every hour forever on a 1 vCPU
// box (AGENTS.md §4.G).
const AntigravityReleasePollInterval = time.Hour

// antigravityStableVersion reads the version out of the updater's prose.
//
// Anchored on "stable version" rather than any number in the page, so a
// rollout percentage or an unrelated figure cannot be mistaken for a release.
var antigravityStableVersion = regexp.MustCompile(`(?i)stable\s+version\s*:?\s*v?(\d+(?:\.\d+)*)`)

// antigravityRollout reads the rollout percentage, which gates whether a
// release is reaching accounts yet.
var antigravityRollout = regexp.MustCompile(`(?i)rolled\s+out\s+to\s+(\d+(?:\.\d+)?)\s*%`)

// AntigravityRelease is what the updater service reports.
type AntigravityRelease struct {
	// Version is the stable version, or "" when the response could not be read.
	Version string
	// RolloutPercent is how widely it is being served; -1 when absent.
	RolloutPercent float64
}

// ParseAntigravityRelease reads the updater service's response.
//
// The response is prose, not JSON, so the wording can change without notice.
// An unreadable body therefore yields an empty Version rather than an error
// the caller might act on: no version means no drift, never a halt. Treating a
// reworded page as a release would halt the provider for a copy edit.
func ParseAntigravityRelease(body string) AntigravityRelease {
	rel := AntigravityRelease{RolloutPercent: -1}
	if m := antigravityStableVersion.FindStringSubmatch(body); m != nil {
		rel.Version = m[1]
	}
	if m := antigravityRollout.FindStringSubmatch(body); m != nil {
		if pct, err := strconv.ParseFloat(m[1], 64); err == nil {
			rel.RolloutPercent = pct
		}
	}
	return rel
}

var (
	antigravityReleaseMu   sync.RWMutex
	antigravityLastRelease AntigravityRelease
	antigravityLastSeenAt  time.Time
)

// LastAntigravityRelease returns the most recent poll result and when it was
// taken. A zero time means the service has not answered yet.
func LastAntigravityRelease() (AntigravityRelease, time.Time) {
	antigravityReleaseMu.RLock()
	defer antigravityReleaseMu.RUnlock()
	return antigravityLastRelease, antigravityLastSeenAt
}

// ResetAntigravityRelease clears the remembered poll result. For tests, and for
// an operator who patched the pinned identity.
func ResetAntigravityRelease() {
	antigravityReleaseMu.Lock()
	antigravityLastRelease = AntigravityRelease{RolloutPercent: -1}
	antigravityLastSeenAt = time.Time{}
	antigravityReleaseMu.Unlock()
}

// FetchAntigravityRelease asks the updater service once.
func FetchAntigravityRelease(ctx context.Context, client *http.Client) (AntigravityRelease, error) {
	return fetchAntigravityReleaseFrom(ctx, client, AntigravityUpdaterURL)
}

// fetchAntigravityReleaseFrom is FetchAntigravityRelease with the endpoint as a
// parameter, so a test exercises this exact code against a fake server instead
// of a copy of it. The exported wrapper pins the real URL.
func fetchAntigravityReleaseFrom(ctx context.Context, client *http.Client, url string) (AntigravityRelease, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return AntigravityRelease{RolloutPercent: -1}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return AntigravityRelease{RolloutPercent: -1}, err
	}
	defer resp.Body.Close()

	// Bounded read: the body is one sentence, and an unbounded copy from a
	// remote host is a memory risk on the deployment target (§4.G).
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return AntigravityRelease{RolloutPercent: -1}, err
	}

	rel := ParseAntigravityRelease(string(body))

	antigravityReleaseMu.Lock()
	antigravityLastRelease = rel
	antigravityLastSeenAt = time.Now().UTC()
	antigravityReleaseMu.Unlock()

	return rel, nil
}

// AntigravityReleaseDrift reports whether the released version is ahead of the
// identity this gateway sends.
//
// Only a released version strictly ahead of the pin counts. An equal or older
// one is the healthy state, and an unreadable one is not drift — see
// ParseAntigravityRelease.
func AntigravityReleaseDrift(rel AntigravityRelease) AntigravityDrift {
	d := AntigravityDrift{Pinned: AntigravityCLIVersion}
	if rel.Version == "" {
		return d
	}
	d.Observed = rel.Version
	d.Newer = CompareVersions(rel.Version, AntigravityCLIVersion) > 0
	return d
}

// StartAntigravityReleaseWatch polls the updater service and calls onDrift when
// the released version moves ahead of the pinned identity.
//
// onDrift is called once per distinct version, not once per poll: the halt it
// performs is already latched, and repeating it would rewrite the record every
// hour for a fact that has not changed.
//
// The caller owns what drift means. This package knows the versions; it does
// not know about providers or latches, so halting stays with the wiring layer.
func StartAntigravityReleaseWatch(ctx context.Context, client *http.Client, onDrift func(AntigravityDrift, AntigravityRelease)) {
	go func() {
		// Boot delay, matching StartBackgroundCatalogSync: a gateway starting
		// up has more urgent work than an outbound probe.
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}

		var reported string
		poll := func() {
			rel, err := FetchAntigravityRelease(ctx, client)
			if err != nil {
				// A failed probe is not drift. The service being unreachable
				// says nothing about the client version, so halting on it
				// would take the provider down for a network blip.
				log.Warn("antigravity_watch", "release poll failed", "error", err)
				return
			}
			if rel.Version == "" {
				log.Warn("antigravity_watch", "updater response carried no version; the wording may have changed",
					"url", AntigravityUpdaterURL)
				return
			}
			d := AntigravityReleaseDrift(rel)
			if !d.Newer || d.Observed == reported {
				return
			}
			reported = d.Observed
			log.Warn("antigravity_watch", "released client is ahead of the pinned identity",
				"released", d.Observed, "pinned", d.Pinned, "rollout_percent", rel.RolloutPercent)
			if onDrift != nil {
				onDrift(d, rel)
			}
		}

		poll()
		ticker := time.NewTicker(AntigravityReleasePollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				poll()
			}
		}
	}()
}

// AntigravityDriftReason is the halt reason recorded for a version drift. It
// names the two versions so an operator reading the dashboard sees what to
// patch, and is prefixed so it cannot be mistaken for an upstream message.
func AntigravityDriftReason(d AntigravityDrift, rel AntigravityRelease) string {
	var b strings.Builder
	b.WriteString("client version drift: Antigravity released ")
	b.WriteString(d.Observed)
	b.WriteString(", this gateway sends ")
	b.WriteString(d.Pinned)
	if rel.RolloutPercent >= 0 {
		b.WriteString(" (rollout ")
		b.WriteString(strconv.FormatFloat(rel.RolloutPercent, 'f', -1, 64))
		b.WriteString("%)")
	}
	b.WriteString(". Accounts are not being spent on a stale fingerprint; patch the pinned identity, then clear the halt")
	return b.String()
}
