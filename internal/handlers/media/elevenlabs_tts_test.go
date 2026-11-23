package media

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/providers"
)

type elevenLabsTTSUpstream struct {
	server    *httptest.Server
	path      string
	header    http.Header
	body      map[string]any
	audio     []byte
	status    int
	respAudio []byte
	calls     int
}

func newElevenLabsTTSUpstream(t *testing.T, status int) *elevenLabsTTSUpstream {
	t.Helper()
	up := &elevenLabsTTSUpstream{
		header:    http.Header{},
		status:    status,
		respAudio: []byte(strings.Repeat("a", 4096)),
	}
	up.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up.calls++
		up.path = r.URL.Path
		for k, v := range r.Header {
			if len(v) > 0 {
				up.header[k] = v
			}
		}
		body, _ := io.ReadAll(r.Body)
		up.body = map[string]any{}
		_ = json.Unmarshal(body, &up.body)
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(up.status)
		if up.status == http.StatusOK {
			_, _ = w.Write(up.respAudio)
			return
		}
		_, _ = w.Write([]byte(`{"detail":{"message":"nope"}}`))
	}))
	t.Cleanup(up.server.Close)
	return up
}

func TestHandleTTS_ElevenLabs_VendorWireContract(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		voice      string
		wantPath   string
		wantModel  string
		wantVoice  string
		wantModel_ string // model_id in body when voice param carries the voice
	}{
		{
			name:      "model/voice splits into model_id and voice_id path",
			model:     "el/eleven_multilingual_v2/21m00Tcm4TlvDq8ikWAM",
			wantPath:  "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
			wantModel: "eleven_multilingual_v2",
		},
		{
			name:      "voice param wins over model remainder",
			model:     "el/eleven_multilingual_v2",
			voice:     "21m00Tcm4TlvDq8ikWAM",
			wantPath:  "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
			wantModel: "eleven_multilingual_v2",
		},
		{
			name:      "bare model falls back to default eleven_flash_v2_5",
			model:     "el/21m00Tcm4TlvDq8ikWAM",
			wantPath:  "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
			wantModel: elevenLabsDefaultTTSModel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up := newElevenLabsTTSUpstream(t, http.StatusOK)
			overrideElevenLabsProvider(t, providers.ProviderConfig{
				BaseURL:    "https://api.elevenlabs.io",
				AuthHeader: "xi-api-key",
				AuthScheme: "raw",
				TTSURL:     up.server.URL + "/v1/text-to-speech",
			})
			handler := newElevenLabsSTTHandler(t)

			payload, _ := json.Marshal(map[string]any{
				"model": tc.model,
				"input": "hello",
				"voice": tc.voice,
			})
			req := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(string(payload)))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleAudioSpeech(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if up.calls != 1 {
				t.Fatalf("expected 1 upstream call, got %d", up.calls)
			}
			if up.path != tc.wantPath {
				t.Errorf("upstream path = %q, want %q", up.path, tc.wantPath)
			}
			if got := up.header.Get("xi-api-key"); got != "sk-el-TEST" {
				t.Errorf("expected xi-api-key sk-el-TEST, got %q", got)
			}
			if got := up.body["model_id"]; got != tc.wantModel {
				t.Errorf("body model_id = %v, want %q", got, tc.wantModel)
			}
			if got := up.body["text"]; got != "hello" {
				t.Errorf("body text = %v, want %q", got, "hello")
			}
			settings, ok := up.body["voice_settings"].(map[string]any)
			if !ok {
				t.Fatalf("body voice_settings missing: %v", up.body)
			}
			if settings["stability"] != 0.5 || settings["similarity_boost"] != 0.75 {
				t.Errorf("voice_settings = %v, want stability 0.5 similarity_boost 0.75", settings)
			}
		})
	}
}

func TestHandleTTS_ElevenLabs_ErrorSurfaces(t *testing.T) {
	up := newElevenLabsTTSUpstream(t, http.StatusUnauthorized)
	overrideElevenLabsProvider(t, providers.ProviderConfig{
		BaseURL:    "https://api.elevenlabs.io",
		AuthHeader: "xi-api-key",
		AuthScheme: "raw",
		TTSURL:     up.server.URL + "/v1/text-to-speech",
	})
	handler := newElevenLabsSTTHandler(t)

	payload, _ := json.Marshal(map[string]any{
		"model": "el/eleven_multilingual_v2/21m00Tcm4TlvDq8ikWAM",
		"input": "hello",
	})
	req := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleAudioSpeech(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected failure, got 200")
	}
	if !strings.Contains(rec.Body.String(), "nope") {
		t.Errorf("expected upstream error body surfaced, got %s", rec.Body.String())
	}
}

func TestParseElevenLabsModelVoice(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		voice     string
		wantModel string
		wantVoice string
	}{
		{"model/voice", "eleven_multilingual_v2/VOICE1", "", "eleven_multilingual_v2", "VOICE1"},
		{"voice param wins", "eleven_multilingual_v2", "VOICE2", "eleven_multilingual_v2", "VOICE2"},
		{"bare value is voice with default model", "VOICE3", "", elevenLabsDefaultTTSModel, "VOICE3"},
		{"alloy voice param ignored", "eleven_turbo_v2_5/VOICE4", "alloy", "eleven_turbo_v2_5", "VOICE4"},
		{"trailing slash falls back", "VOICE5/", "", elevenLabsDefaultTTSModel, "VOICE5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotModel, gotVoice := parseElevenLabsModelVoice(tc.model, tc.voice)
			if gotModel != tc.wantModel || gotVoice != tc.wantVoice {
				t.Errorf("parseElevenLabsModelVoice(%q, %q) = (%q, %q), want (%q, %q)",
					tc.model, tc.voice, gotModel, gotVoice, tc.wantModel, tc.wantVoice)
			}
		})
	}
}
