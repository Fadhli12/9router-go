package providers

import "testing"

func TestNormalizeAntigravityClientProfile(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  AntigravityClientProfile
	}{
		{name: "ide", input: "ide", want: AntigravityProfileIDE},
		{name: "cli", input: "cli", want: AntigravityProfileCLI},
		{name: "uppercase cli", input: "CLI", want: AntigravityProfileCLI},
		{name: "whitespace", input: "  cli  ", want: AntigravityProfileCLI},
		{name: "legacy harness", input: "harness", want: AntigravityProfileCLI},
		{name: "legacy sdk", input: "sdk", want: AntigravityProfileCLI},
		{name: "empty string", input: "", want: AntigravityProfileIDE},
		{name: "unknown value", input: "something-else", want: AntigravityProfileIDE},
		{name: "nil", input: nil, want: AntigravityProfileIDE},
		{name: "non-string", input: 42, want: AntigravityProfileIDE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAntigravityClientProfile(tt.input); got != tt.want {
				t.Errorf("NormalizeAntigravityClientProfile(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsAntigravityClientProfile(t *testing.T) {
	valid := []string{"ide", "cli", "harness", "sdk", "IDE", "CLI"}
	for _, v := range valid {
		if !IsAntigravityClientProfile(v) {
			t.Errorf("IsAntigravityClientProfile(%q) = false, want true", v)
		}
	}
	invalid := []any{"", "unknown", nil, 42, " id "}
	for _, v := range invalid {
		if IsAntigravityClientProfile(v) {
			t.Errorf("IsAntigravityClientProfile(%v) = true, want false", v)
		}
	}
}

func TestAntigravityUserAgentFor(t *testing.T) {
	ide := AntigravityUserAgentFor(AntigravityProfileIDE)
	if ide != "antigravity/hub/"+AntigravityIDEVersion+" (os_type=windows; arch=amd64)" {
		t.Errorf("IDE user agent = %q, want the pinned hub shape", ide)
	}
	cli := AntigravityUserAgentFor(AntigravityProfileCLI)
	if cli != AntigravityCLIUserAgent {
		t.Errorf("CLI user agent = %q, want %q", cli, AntigravityCLIUserAgent)
	}
	// Both lanes present the Hub fingerprint since the port of upstream #4816,
	// so identical strings are the expected state today — the profile stays
	// honoured for the day the lanes diverge again.
	// An unknown profile falls back to IDE through Normalize, and the
	// function itself must behave the same way.
	if got := AntigravityUserAgentFor("bogus"); got != ide {
		t.Errorf("unknown profile user agent = %q, want the IDE shape %q", got, ide)
	}
}

func TestAntigravityClientProfileString(t *testing.T) {
	if got := AntigravityProfileCLI.String(); got != "cli" {
		t.Errorf("String() = %q, want cli", got)
	}
	if got := AntigravityProfileIDE.String(); got != "ide" {
		t.Errorf("String() = %q, want ide", got)
	}
}
