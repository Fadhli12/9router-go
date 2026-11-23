package media

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderFromVoicesPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/api/media-providers/tts/elevenlabs/voices", "elevenlabs"},
		{"/api/media-providers/tts/deepgram/voices", "deepgram"},
		{"/api/media-providers/tts/inworld/voices", "inworld"},
		{"/api/media-providers/tts/minimax/voices", "minimax"},
		{"/api/media-providers/tts/voices", ""},
		{"/audio/voices", ""},
	}
	for _, tc := range tests {
		if got := providerFromVoicesPath(tc.path); got != tc.want {
			t.Errorf("providerFromVoicesPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// The provider-specific voices routes carry the provider in the path and no
// ?provider= query; the handler must not fall back to edge-tts for them.
func TestHandleAudioVoices_ElevenLabsPath_SelectsElevenLabs(t *testing.T) {
	handler := newElevenLabsSTTHandler(t)

	req := httptest.NewRequest("GET", "/api/media-providers/tts/elevenlabs/voices", nil)
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	handler.HandleAudioVoices(rec, req)

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("provider-specific path was not resolved: %s", rec.Body.String())
	}
}
