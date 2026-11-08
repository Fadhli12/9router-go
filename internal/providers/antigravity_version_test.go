package providers

import "testing"

// The gateway presents a pinned Antigravity fingerprint upstream. When Google
// ships a release this build does not match, the pooled accounts are what pay
// for it — the operator's pool was spent on exactly that event. These tests pin
// the detection that ends the rotation on the first upgraded client instead of
// the fortieth account.

func TestParseAntigravityClientVersion(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{
			name:      "cli shape, the identity the inference path sends",
			userAgent: "antigravity/cli/1.5.1 (aidev_client; os_type=windows; arch=amd64; cl=1014269460; auth_method=consumer)",
			want:      "1.5.1",
		},
		{
			name:      "ide shape",
			userAgent: "antigravity/2.23.0",
			want:      "2.23.0",
		},
		{
			name:      "a v prefix is tolerated",
			userAgent: "antigravity/cli/v1.6.0",
			want:      "1.6.0",
		},
		{
			name:      "two-field version",
			userAgent: "antigravity/1.6",
			want:      "1.6",
		},
		{
			name:      "four-field version",
			userAgent: "antigravity/cli/1.5.1.2",
			want:      "1.5.1.2",
		},
		{
			name:      "case is not significant",
			userAgent: "Antigravity/CLI/1.7.0",
			want:      "1.7.0",
		},
		// The gateway serves many clients; only an Antigravity one may move
		// this needle. A foreign agent that happens to mention a version must
		// not be read as an Antigravity release.
		{name: "a different client", userAgent: "codex_cli_rs/0.159.0", want: ""},
		{name: "a browser", userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", want: ""},
		{name: "grok", userAgent: "grok-shell/1.0.44 (linux; x86_64)", want: ""},
		{name: "empty", userAgent: "", want: ""},
		{name: "the word alone, no version", userAgent: "antigravity", want: ""},
		// "antigravity-cockpit-tools" is the Trae and Windsurf OAuth identity
		// (handlers/oauth/*.go). It is not an Antigravity client release.
		{name: "cockpit tools is not a release", userAgent: "antigravity-cockpit-tools", want: ""},
		{name: "trae borrows the word", userAgent: "Trae/1.0.0 antigravity-cockpit-tools", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseAntigravityClientVersion(tt.userAgent); got != tt.want {
				t.Errorf("ParseAntigravityClientVersion(%q) = %q, want %q", tt.userAgent, got, tt.want)
			}
		})
	}
}

// String comparison orders versions wrongly exactly where it matters, so the
// ordering is field-wise numeric: "1.10.0" is ahead of "1.9.0", not behind it.
func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{a: "1.5.1", b: "1.5.1", want: 0},
		{a: "1.5.2", b: "1.5.1", want: 1},
		{a: "1.5.1", b: "1.5.2", want: -1},
		// The case a lexical sort gets wrong.
		{a: "1.10.0", b: "1.9.0", want: 1},
		{a: "1.9.0", b: "1.10.0", want: -1},
		{a: "2.0.0", b: "1.99.99", want: 1},
		// A missing field is zero, so these are the same version.
		{a: "1.5", b: "1.5.0", want: 0},
		{a: "1.5.1", b: "1.5", want: 1},
		{a: "v1.6.0", b: "1.6.0", want: 0},
		{a: " 1.6.0 ", b: "1.6.0", want: 0},
		// An unreadable field gives up and reports equal: an unparseable
		// version must never be read as an upgrade.
		{a: "1.5.1-beta", b: "1.5.1", want: 0},
		{a: "not-a-version", b: "1.5.1", want: 0},
		{a: "", b: "1.5.1", want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			if got := CompareVersions(tt.a, tt.b); got != tt.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// Only a client ahead of the pinned identity means Google shipped a release
// this build does not match. An older client is an operator who has not
// updated, and halting the provider for that would be a false alarm that costs
// availability for every other account.
func TestObserveAntigravityClientVersionDirection(t *testing.T) {
	t.Run("a newer client is drift", func(t *testing.T) {
		ResetObservedAntigravityVersion()
		t.Cleanup(ResetObservedAntigravityVersion)

		d := ObserveAntigravityClientVersion("antigravity/cli/99.0.0 (aidev_client)")
		if !d.Newer {
			t.Errorf("Newer = false for a client ahead of the pinned %s", AntigravityCLIVersion)
		}
		if d.Observed != "99.0.0" {
			t.Errorf("Observed = %q, want 99.0.0", d.Observed)
		}
		if d.Pinned != AntigravityCLIVersion {
			t.Errorf("Pinned = %q, want %q", d.Pinned, AntigravityCLIVersion)
		}
	})

	t.Run("the pinned version itself is not drift", func(t *testing.T) {
		ResetObservedAntigravityVersion()
		t.Cleanup(ResetObservedAntigravityVersion)

		if ObserveAntigravityClientVersion("antigravity/cli/" + AntigravityCLIVersion).Newer {
			t.Error("Newer = true for a client matching the pinned identity")
		}
	})

	t.Run("an older client is not drift", func(t *testing.T) {
		ResetObservedAntigravityVersion()
		t.Cleanup(ResetObservedAntigravityVersion)

		// 1.0.0 is below any shipped pin; an operator lagging a release must
		// not halt the provider for everyone else.
		if ObserveAntigravityClientVersion("antigravity/cli/1.0.0").Newer {
			t.Error("Newer = true for a client behind the pinned identity")
		}
	})

	t.Run("a non-Antigravity client reports nothing", func(t *testing.T) {
		ResetObservedAntigravityVersion()
		t.Cleanup(ResetObservedAntigravityVersion)

		d := ObserveAntigravityClientVersion("codex_cli_rs/0.159.0")
		if d.Newer || d.Observed != "" {
			t.Errorf("foreign client produced drift: %+v", d)
		}
		if HighestObservedAntigravityVersion() != "" {
			t.Errorf("a foreign client was recorded: %q", HighestObservedAntigravityVersion())
		}
	})
}

// The halt decision must not depend on which client happened to call last: a
// pool mixing an upgraded client with older ones has still seen the release.
func TestObserveAntigravityClientVersionKeepsTheHighest(t *testing.T) {
	ResetObservedAntigravityVersion()
	t.Cleanup(ResetObservedAntigravityVersion)

	ObserveAntigravityClientVersion("antigravity/cli/99.0.0")
	// An older client calling afterwards must not clear the drift.
	d := ObserveAntigravityClientVersion("antigravity/cli/1.0.0")

	if !d.Newer {
		t.Error("Newer = false after an older client called; the drift was forgotten")
	}
	if got := HighestObservedAntigravityVersion(); got != "99.0.0" {
		t.Errorf("highest observed = %q, want 99.0.0", got)
	}
}

// A gateway with no Antigravity client pointed at it has observed nothing, and
// must not report drift from absence.
func TestObserveAntigravityClientVersionSilentByDefault(t *testing.T) {
	ResetObservedAntigravityVersion()
	t.Cleanup(ResetObservedAntigravityVersion)

	if got := HighestObservedAntigravityVersion(); got != "" {
		t.Errorf("highest observed = %q on a fresh gateway, want empty", got)
	}
	if ObserveAntigravityClientVersion("").Newer {
		t.Error("an empty User-Agent produced drift")
	}
}

// The pinned constants are what the detection compares against, so a bump that
// leaves them unparseable would silently disable it: CompareVersions gives up
// and reports equal on a field it cannot read.
func TestPinnedAntigravityVersionsAreComparable(t *testing.T) {
	for name, v := range map[string]string{
		"AntigravityCLIVersion": AntigravityCLIVersion,
		"AntigravityIDEVersion": AntigravityIDEVersion,
	} {
		if v == "" {
			t.Errorf("%s is empty", name)
			continue
		}
		// A version must order strictly below a clearly higher one. If it does
		// not, the field is unparseable and drift can never be detected.
		if CompareVersions(v, "9999.0.0") != -1 {
			t.Errorf("%s = %q does not compare as a version; drift detection would be silently disabled", name, v)
		}
	}
}
