package media

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

type elevenLabsTTSUpstream struct {
	server    *httptest.Server
	path      string
	query     string
	header    http.Header
	body      map[string]any
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
		up.query = r.URL.RawQuery
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
		name          string
		model         string
		voice         string
		extra         map[string]any
		wantPath      string
		wantModel     string
		wantLangCode  string
		wantStability float64
		hasSettings   bool
	}{
		{
			name:      "model/voice splits into model_id and voice_id path",
			model:     "el/eleven_multilingual_v2/21m00Tcm4TlvDq8ikWAM",
			wantPath:  "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
			wantModel: "eleven_multilingual_v2",
		},
		{
			name:      "voice param pairs with resolved model",
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
		{
			name:  "forwards optional language and voice_settings when provided",
			model: "el/eleven_v4",
			voice: "21m00Tcm4TlvDq8ikWAM",
			extra: map[string]any{
				"language": "en",
				"voice_settings": map[string]any{
					"stability":        0.7,
					"similarity_boost": 0.8,
				},
			},
			wantPath:      "/v1/text-to-speech/21m00Tcm4TlvDq8ikWAM",
			wantModel:     "eleven_v4",
			wantLangCode:  "en",
			wantStability: 0.7,
			hasSettings:   true,
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

			reqMap := map[string]any{
				"model": tc.model,
				"input": "hello",
				"voice": tc.voice,
			}
			for k, v := range tc.extra {
				reqMap[k] = v
			}
			payload, _ := json.Marshal(reqMap)
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
			if tc.wantLangCode != "" {
				if got := up.body["language_code"]; got != tc.wantLangCode {
					t.Errorf("body language_code = %v, want %q", got, tc.wantLangCode)
				}
			}
			settings, ok := up.body["voice_settings"].(map[string]any)
			if tc.hasSettings {
				if !ok || settings["stability"] != tc.wantStability {
					t.Errorf("voice_settings = %v, want stability %v", up.body["voice_settings"], tc.wantStability)
				}
			} else if ok {
				t.Errorf("voice_settings should not be forced when omitted, got %v", settings)
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
		{"model/voice wins over voice param", "eleven_v4/inModel", "voice123", "eleven_v4", "inModel"},
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

func TestElevenLabsTTS_VoiceAndFormat(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload["model_id"] != "eleven_v4" {
			t.Errorf("expected model_id eleven_v4, got %v", payload["model_id"])
		}
		if _, ok := payload["voice_settings"]; ok {
			t.Errorf("voice_settings should not be forced")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer up.Close()

	database, cleanup := setupMultimodalTestDB(t)
	defer cleanup()

	connData, _ := json.Marshal(map[string]any{"apiKey": "sk-el", "baseUrl": up.URL})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-el-tts', 'elevenlabs', 'apikey', 'EL TTS', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(connData)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	repo := db.NewRepo(database)
	handler := newTestMediaHandler(repo)

	// bare model with OpenAI voice field
	req := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{"model":"elevenlabs/eleven_v4","input":"Hello","voice":"voice123","response_format":"mp3"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleAudioSpeech(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("content-type = %q, want audio/mpeg", ct)
	}

	// voice inside model string
	req2 := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{"model":"elevenlabs/eleven_v4/inModel","input":"Hello","voice":"voice123","response_format":"opus"}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.HandleAudioSpeech(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if ct := rec2.Header().Get("Content-Type"); ct != "audio/opus" {
		t.Errorf("content-type = %q, want audio/opus", ct)
	}
}

func TestElevenLabsTTS_BareVoiceFallback(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload["model_id"] != "eleven_flash_v2_5" {
			t.Errorf("expected fallback model_id eleven_flash_v2_5, got %v", payload["model_id"])
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer up.Close()

	database, cleanup := setupMultimodalTestDB(t)
	defer cleanup()

	connData, _ := json.Marshal(map[string]any{"apiKey": "sk-el", "baseUrl": up.URL})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-el-tts2', 'elevenlabs', 'apikey', 'EL TTS2', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(connData)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	repo := db.NewRepo(database)
	handler := newTestMediaHandler(repo)

	req := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{"model":"elevenlabs/voice123","input":"Hello","response_format":"json"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleAudioSpeech(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}
