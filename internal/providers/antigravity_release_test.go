package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The updater service answers prose, not JSON, so the wording can change
// without notice. These tests pin the live response verbatim and pin the
// failure direction: an unreadable body must never read as a release, because
// acting on it would halt the provider for a copy edit.
func TestParseAntigravityRelease(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantVersion string
		wantRollout float64
	}{
		{
			// Captured live 2026-11-08.
			name:        "the live response",
			body:        "Antigravity CLI auto updater is running! Stable Version: 1.5.3. Rolled out to 100%",
			wantVersion: "1.5.3",
			wantRollout: 100,
		},
		{
			name:        "a partial rollout is read, not rounded away",
			body:        "Antigravity CLI auto updater is running! Stable Version: 1.6.0. Rolled out to 25%",
			wantVersion: "1.6.0",
			wantRollout: 25,
		},
		{
			name:        "a fractional rollout",
			body:        "Stable Version: 1.6.1. Rolled out to 2.5%",
			wantVersion: "1.6.1",
			wantRollout: 2.5,
		},
		{
			name:        "spacing and case are not significant",
			body:        "stable   version   :   v1.7.0 .  rolled   out   to   50 %",
			wantVersion: "1.7.0",
			wantRollout: 50,
		},
		{
			name:        "a version with no rollout line",
			body:        "Stable Version: 1.5.3",
			wantVersion: "1.5.3",
			wantRollout: -1,
		},
		// The failure direction. Each of these must yield no version, so the
		// caller sees no drift rather than a halt.
		{name: "empty body", body: "", wantVersion: "", wantRollout: -1},
		{name: "reworded page", body: "Antigravity CLI updater service OK", wantVersion: "", wantRollout: -1},
		{name: "an error page", body: "<!doctype html><title>404 Not Found</title>", wantVersion: "", wantRollout: -1},
		{
			// A bare number elsewhere in the page is not a release: the regex
			// is anchored on "stable version" for exactly this reason.
			name:        "a loose number is not a version",
			body:        "Antigravity CLI auto updater is running! Rolled out to 100%",
			wantVersion: "",
			wantRollout: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseAntigravityRelease(tt.body)
			if got.Version != tt.wantVersion {
				t.Errorf("Version = %q, want %q", got.Version, tt.wantVersion)
			}
			if got.RolloutPercent != tt.wantRollout {
				t.Errorf("RolloutPercent = %v, want %v", got.RolloutPercent, tt.wantRollout)
			}
		})
	}
}

// Only a release strictly ahead of the pinned identity is drift. Equal is the
// healthy state, and an older release would mean the pin is ahead of Google —
// which is not what this watch is for.
func TestAntigravityReleaseDrift(t *testing.T) {
	t.Run("a newer release is drift", func(t *testing.T) {
		d := AntigravityReleaseDrift(AntigravityRelease{Version: "99.0.0", RolloutPercent: 100})
		if !d.Newer {
			t.Errorf("Newer = false for 99.0.0 against the pinned %s", AntigravityCLIVersion)
		}
		if d.Observed != "99.0.0" || d.Pinned != AntigravityCLIVersion {
			t.Errorf("drift = %+v", d)
		}
	})

	t.Run("the pinned version is not drift", func(t *testing.T) {
		if AntigravityReleaseDrift(AntigravityRelease{Version: AntigravityCLIVersion}).Newer {
			t.Error("Newer = true for the pinned version")
		}
	})

	t.Run("an older release is not drift", func(t *testing.T) {
		if AntigravityReleaseDrift(AntigravityRelease{Version: "0.1.0"}).Newer {
			t.Error("Newer = true for a release behind the pin")
		}
	})

	t.Run("an unreadable response is not drift", func(t *testing.T) {
		d := AntigravityReleaseDrift(AntigravityRelease{Version: "", RolloutPercent: -1})
		if d.Newer {
			t.Error("Newer = true with no version read; a reworded page would halt the provider")
		}
	})
}

// The fetch path goes through a fake service: no test may depend on Google's
// endpoint being reachable, or the suite fails offline and in CI.
func TestFetchAntigravityRelease(t *testing.T) {
	t.Run("reads the version and records it", func(t *testing.T) {
		ResetAntigravityRelease()
		t.Cleanup(ResetAntigravityRelease)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("Antigravity CLI auto updater is running! Stable Version: 1.9.9. Rolled out to 100%"))
		}))
		t.Cleanup(srv.Close)

		rel, err := fetchFrom(t, srv.URL)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if rel.Version != "1.9.9" {
			t.Errorf("Version = %q, want 1.9.9", rel.Version)
		}
		if rel.RolloutPercent != 100 {
			t.Errorf("RolloutPercent = %v, want 100", rel.RolloutPercent)
		}
	})

	t.Run("a server error yields no version rather than a false release", func(t *testing.T) {
		ResetAntigravityRelease()
		t.Cleanup(ResetAntigravityRelease)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("<!doctype html><title>500</title>"))
		}))
		t.Cleanup(srv.Close)

		rel, err := fetchFrom(t, srv.URL)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if rel.Version != "" {
			t.Errorf("Version = %q from an error page, want empty", rel.Version)
		}
		if AntigravityReleaseDrift(rel).Newer {
			t.Error("an error page produced drift")
		}
	})

	t.Run("a body larger than the cap does not stall the read", func(t *testing.T) {
		ResetAntigravityRelease()
		t.Cleanup(ResetAntigravityRelease)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("Stable Version: 2.0.0. Rolled out to 100%"))
			for range 4096 {
				_, _ = w.Write([]byte("padding padding padding padding "))
			}
		}))
		t.Cleanup(srv.Close)

		rel, err := fetchFrom(t, srv.URL)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if rel.Version != "2.0.0" {
			t.Errorf("Version = %q, want 2.0.0", rel.Version)
		}
	})
}

// fetchFrom drives the production fetch path against a fake server, so the
// request, the bounded read and the parse under test are the ones that ship.
func fetchFrom(t *testing.T, url string) (AntigravityRelease, error) {
	t.Helper()
	return fetchAntigravityReleaseFrom(context.Background(), srvClient(), url)
}

func srvClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// The halt reason is what an operator reads in the dashboard, so it has to name
// both versions: the released one and the one this build sends.
func TestAntigravityDriftReason(t *testing.T) {
	d := AntigravityDrift{Observed: "1.5.3", Pinned: "1.5.1"}

	withRollout := AntigravityDriftReason(d, AntigravityRelease{Version: "1.5.3", RolloutPercent: 100})
	for _, want := range []string{"1.5.3", "1.5.1", "100%", "clear the halt"} {
		if !strings.Contains(withRollout, want) {
			t.Errorf("reason %q does not mention %q", withRollout, want)
		}
	}

	// An absent rollout must not print a bare "-1%".
	withoutRollout := AntigravityDriftReason(d, AntigravityRelease{Version: "1.5.3", RolloutPercent: -1})
	if strings.Contains(withoutRollout, "-1") {
		t.Errorf("reason leaked the sentinel rollout: %q", withoutRollout)
	}
}

// The poll interval is a request every hour forever on a 1 vCPU box, so a later
// edit that makes it aggressive should have to argue with this.
func TestAntigravityReleasePollIntervalIsModest(t *testing.T) {
	if AntigravityReleasePollInterval < 15*time.Minute {
		t.Errorf("poll interval %v is under 15m; a release is a human-scale event", AntigravityReleasePollInterval)
	}
}
