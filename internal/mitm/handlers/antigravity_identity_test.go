package handlers

import (
	"testing"
)

// The MITM handler overwrites the Antigravity client's User-Agent with the
// gateway's pinned identity. That overwrite is deliberate, but it used to
// discard the genuine fingerprint in the process — and that fingerprint is the
// only thing a version bump can be built from, because `cl` comes from the
// client's runtime config rather than its binary and so cannot be read out of
// the installed executable.
//
// These tests pin the reader that captures it before the overwrite runs.

func TestAntigravityBodyIDEVersion(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			name: "top-level metadata, the IDE shape",
			body: map[string]any{
				"metadata": map[string]any{"ideVersion": "2.23.0"},
			},
			want: "2.23.0",
		},
		{
			// Some payloads nest metadata inside "request"; the overwrite path
			// already handles both, so the reader has to as well.
			name: "nested under request",
			body: map[string]any{
				"request": map[string]any{
					"metadata": map[string]any{"ideVersion": "2.24.1"},
				},
			},
			want: "2.24.1",
		},
		{
			name: "top level wins when both are present",
			body: map[string]any{
				"metadata": map[string]any{"ideVersion": "2.23.0"},
				"request": map[string]any{
					"metadata": map[string]any{"ideVersion": "9.9.9"},
				},
			},
			want: "2.23.0",
		},
		{name: "no metadata at all", body: map[string]any{"model": "gemini-3.8-flash"}, want: ""},
		{name: "metadata without ideVersion", body: map[string]any{"metadata": map[string]any{"other": "x"}}, want: ""},
		{name: "empty ideVersion is not a version", body: map[string]any{"metadata": map[string]any{"ideVersion": ""}}, want: ""},
		{name: "empty body", body: map[string]any{}, want: ""},
		// A non-string value must not panic the capture path: the body is
		// attacker-shaped as far as this code knows, it comes off the wire.
		{name: "ideVersion is not a string", body: map[string]any{"metadata": map[string]any{"ideVersion": 223}}, want: ""},
		{name: "metadata is not a map", body: map[string]any{"metadata": "nope"}, want: ""},
		{name: "request is not a map", body: map[string]any{"request": 7}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := antigravityBodyIDEVersion(tt.body); got != tt.want {
				t.Errorf("antigravityBodyIDEVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
