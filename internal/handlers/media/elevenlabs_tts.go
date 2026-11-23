package media

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"

	"9router/proxy/internal/handlers/chat"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/log"
	"9router/proxy/internal/models"
	"9router/proxy/internal/providers"
)

// ElevenLabs TTS adapter (upstream parity: open-sse/handlers/ttsProviders/elevenlabs.js).
// The vendor's API is not OpenAI-shaped: the voice id rides the URL path
// (/v1/text-to-speech/{voice_id}) and the body is {text, model_id, voice_settings},
// so the generic media funnel would post an OpenAI body to a path ElevenLabs
// never serves (404 {"detail":"Not Found"}).
//
// Model wire convention (same as upstream): the resolved model carries either
// "model_id/voice_id" (e.g. el/eleven_multilingual_v2/<voice_id>) or a bare
// voice_id, in which case the default model eleven_flash_v2_5 is used.

const elevenLabsDefaultTTSModel = "eleven_flash_v2_5"

func (h *MediaHandler) forwardElevenLabsTTS(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo, input, voice, respFmt string) {
	preferredConnID := r.Header.Get("x-connection-id")
	if preferredConnID == "" {
		preferredConnID = r.Header.Get("x-provider-connection-id")
	}
	if preferredConnID == "" {
		preferredConnID = modelInfo.ConnectionID
	}
	// Upstream parity (tts.js credential loop): rotate accounts, lock failures.
	usePinned := preferredConnID != ""
	excludeIDs := []string{}
	var lastStatus int
	var lastMsg string
	for {
		conn, connData, err := h.ChatH.GetBestConnectionWithContext(r.Context(), "elevenlabs", preferredConnID, excludeIDs, modelInfo.Model)
		if err != nil || connData == nil {
			if lastMsg != "" {
				handlerutil.WriteJSONError(w, lastStatus, lastMsg)
				return
			}
			handlerutil.WriteJSONError(w, http.StatusNotFound, "no active connections for provider: elevenlabs")
			return
		}
		status, msg, done := h.tryElevenLabsTTSConn(w, r, body, modelInfo, input, voice, respFmt, conn, connData)
		if done {
			return
		}
		lastStatus, lastMsg = status, msg
		if usePinned {
			handlerutil.WriteJSONError(w, status, msg)
			return
		}
		excludeIDs = append(excludeIDs, conn.ID)
	}
}

// parseElevenLabsModelVoice splits the resolved model into (model_id, voice_id),
// mirroring upstream's adapter: "model/voice" wins, a bare value is the voice
// with the default model. The request's OpenAI-style `voice` field takes
// precedence when present, so plain "el/eleven_multilingual_v2" + voice works too.
func parseElevenLabsModelVoice(resolvedModel, voiceParam string) (modelID, voiceID string) {
	if voiceParam = strings.TrimSpace(voiceParam); voiceParam != "" && voiceParam != "alloy" && voiceParam != "default" {
		return strings.TrimSpace(resolvedModel), voiceParam
	}
	if m, v, found := strings.Cut(resolvedModel, "/"); found && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(m), strings.TrimSpace(v)
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(resolvedModel), "/")
	return elevenLabsDefaultTTSModel, trimmed
}

func (h *MediaHandler) tryElevenLabsTTSConn(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo, input, voice, respFmt string, conn *models.ProviderConnection, connData *chat.ConnectionData) (int, string, bool) {
	apiKey := chat.ExtractAPIKey(connData)
	if apiKey == "" {
		return http.StatusUnauthorized, "no API key found for elevenlabs", false
	}

	modelID, voiceID := parseElevenLabsModelVoice(modelInfo.Model, voice)
	if voiceID == "" {
		return http.StatusBadRequest, "missing voice id for elevenlabs TTS", true
	}

	providerCfg, err := h.ChatH.GetProviderConfig("elevenlabs", connData)
	if err != nil {
		return http.StatusInternalServerError, "get elevenlabs config: " + err.Error(), true
	}

	payload, err := json.Marshal(map[string]any{
		"text":     input,
		"model_id": modelID,
		"voice_settings": map[string]float64{
			"stability":        0.5,
			"similarity_boost": 0.75,
		},
	})
	if err != nil {
		return http.StatusInternalServerError, "build elevenlabs tts request: " + err.Error(), true
	}

	ttsURL := elevenLabsTTSURL(connData, providerCfg) + "/" + voiceID
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, ttsURL, bytes.NewReader(payload))
	if err != nil {
		return http.StatusInternalServerError, "create elevenlabs tts request: " + err.Error(), true
	}
	req.Header.Set("Content-Type", "application/json")
	authHeader := providerCfg.AuthHeader
	if authHeader == "" {
		authHeader = "xi-api-key"
	}
	handlerutil.SetAuthHeader(req, apiKey, authHeader, providerCfg.AuthScheme)
	for k, v := range providerCfg.StaticHeaders {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}

	client, clientErr := h.ChatH.GetClientForConnection(connData)
	if clientErr != nil {
		return http.StatusBadGateway, clientErr.Error(), true
	}
	resp, err := client.Do(req)
	if err != nil {
		return http.StatusBadGateway, "elevenlabs tts request failed: " + err.Error(), true
	}
	defer resp.Body.Close()

	audio, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return http.StatusBadGateway, "read elevenlabs response: " + err.Error(), true
	}
	if resp.StatusCode != http.StatusOK {
		errText := strings.TrimSpace(string(audio[:min(300, len(audio))]))
		if errText == "" {
			errText = resp.Status
		}
		log.Warn("media", "elevenlabs tts upstream error", "status", resp.StatusCode, "body", errText)
		return resp.StatusCode, fmt.Sprintf("elevenlabs tts failed with status %d: %s", resp.StatusCode, errText), false
	}
	if len(audio) < 1024 {
		return http.StatusBadGateway, "ElevenLabs TTS returned empty audio", false
	}

	h.writeTTSResponse(w, r, respFmt, audio, "audio/mpeg", "mp3")
	h.trackTTSUsage(modelInfo.Provider, modelInfo.Model)
	if h.Repo != nil {
		h.Repo.UpdateConnectionLastUsed(conn.ID)
	}
	return 0, "", true
}

// elevenLabsTTSURL honors connection and registry base URL overrides the way
// the other elevenlabs lanes do, falling back to the vendor's default host.
func elevenLabsTTSURL(connData *chat.ConnectionData, providerCfg *providers.ProviderConfig) string {
	if providerCfg != nil && strings.TrimSpace(providerCfg.TTSURL) != "" {
		return strings.TrimRight(strings.TrimSpace(providerCfg.TTSURL), "/")
	}
	if connData != nil && strings.TrimSpace(connData.BaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(connData.BaseURL), "/") + "/v1/text-to-speech"
	}
	return "https://api.elevenlabs.io/v1/text-to-speech"
}
