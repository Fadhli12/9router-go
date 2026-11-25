package media

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/url"
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
// voice_id, in which case the default model eleven_flash_v2_5 is used. The
// request's OpenAI-style `voice` field takes precedence when present.

const elevenLabsDefaultTTSModel = "eleven_flash_v2_5"

func (h *MediaHandler) forwardElevenLabsTTS(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo, input, voice, respFmt string) {
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

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
		status, msg, done := h.tryElevenLabsTTSConn(w, r, modelInfo, input, voice, respFmt, raw, conn, connData)
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
	// "model/voice" wins outright (upstream SplitN rule): the voice riding the
	// model string is the explicit vendor addressing, and an OpenAI-style
	// voice field must not override it.
	if m, v, found := strings.Cut(resolvedModel, "/"); found && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(m), strings.TrimSpace(v)
	}
	if voiceParam = strings.TrimSpace(voiceParam); voiceParam != "" && voiceParam != "alloy" && voiceParam != "default" {
		return strings.TrimSpace(resolvedModel), voiceParam
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(resolvedModel), "/")
	return elevenLabsDefaultTTSModel, trimmed
}

func (h *MediaHandler) tryElevenLabsTTSConn(w http.ResponseWriter, r *http.Request, modelInfo *chat.ModelInfo, input, voice, respFmt string, raw map[string]any, conn *models.ProviderConnection, connData *chat.ConnectionData) (int, string, bool) {
	apiKey := chat.ExtractAPIKey(connData)
	if apiKey == "" {
		return http.StatusUnauthorized, "no API key found for elevenlabs", false
	}

	modelID, voiceID := parseElevenLabsModelVoice(modelInfo.Model, voice)
	if voiceID == "" {
		return http.StatusBadRequest, "missing voice id for elevenlabs TTS", true
	}

	// Upstream contract: the audio codec is negotiated through the vendor's
	// output_format query parameter, not the response envelope alone.
	rf := strings.ToLower(strings.TrimSpace(respFmt))
	if rf == "" {
		rf = "mp3"
	}
	type outFmt struct {
		param  string
		format string
		mime   string
	}
	var out outFmt
	switch rf {
	case "opus":
		out = outFmt{"opus_48000_128", "opus", "audio/opus"}
	case "wav":
		out = outFmt{"wav_24000", "wav", "audio/wav"}
	case "pcm":
		out = outFmt{"pcm_24000", "pcm", "audio/pcm"}
	case "json":
		out = outFmt{"mp3_44100_128", "mp3", "audio/mpeg"}
	default:
		out = outFmt{"mp3_44100_128", "mp3", "audio/mpeg"}
	}

	// Upstream contract: the request body is forwarded as the vendor's own
	// shape, with the known tuning keys passed through verbatim rather than
	// forced — a forced voice_settings would silently override the caller's
	// own stability/similarity choices.
	payload := map[string]any{
		"text":     input,
		"model_id": modelID,
	}
	if raw != nil {
		if lang, ok := raw["language"].(string); ok && strings.TrimSpace(lang) != "" {
			payload["language_code"] = strings.TrimSpace(lang)
		}
		for _, key := range []string{"voice_settings", "seed", "apply_text_normalization", "previous_text", "next_text", "pronunciation_dictionary_locators"} {
			if v, ok := raw[key]; ok && v != nil {
				payload[key] = v
			}
		}
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return http.StatusInternalServerError, "build elevenlabs tts request: " + err.Error(), true
	}

	base := elevenLabsTTSURL(h.ChatH, connData)
	if !strings.Contains(base, "text-to-speech") {
		base = strings.TrimRight(base, "/") + "/v1/text-to-speech"
	}
	targetURL := strings.TrimRight(base, "/") + "/" + url.PathEscape(voiceID) + "?output_format=" + url.QueryEscape(out.param)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return http.StatusInternalServerError, "create elevenlabs tts request: " + err.Error(), true
	}
	req.Header.Set("Content-Type", "application/json")

	providerCfg, cfgErr := h.ChatH.GetProviderConfig("elevenlabs", connData)
	if cfgErr != nil {
		return http.StatusInternalServerError, "get elevenlabs config: " + cfgErr.Error(), true
	}
	authHeader := "xi-api-key"
	authScheme := "raw"
	if providerCfg != nil {
		if providerCfg.AuthHeader != "" {
			authHeader = providerCfg.AuthHeader
		}
		authScheme = providerCfg.AuthScheme
	}
	handlerutil.SetAuthHeader(req, apiKey, authHeader, authScheme)
	if providerCfg != nil {
		for k, v := range providerCfg.StaticHeaders {
			req.Header.Set(k, v)
		}
	}

	client, clientErr := h.ChatH.GetClientForConnection(connData)
	if clientErr != nil {
		return http.StatusBadGateway, clientErr.Error(), false
	}
	client = guardMediaRedirects(client)
	resp, err := client.Do(req)
	if err != nil {
		return http.StatusBadGateway, "elevenlabs tts request failed: " + err.Error(), true
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return http.StatusBadGateway, "read elevenlabs response: " + err.Error(), true
	}
	if resp.StatusCode != http.StatusOK {
		snippet := string(respBody[:min(300, len(respBody))])
		msg := snippet
		var parsed map[string]any
		if err := json.Unmarshal(respBody, &parsed); err == nil {
			if d, ok := parsed["detail"].(map[string]any); ok {
				if m, ok := d["message"].(string); ok && m != "" {
					msg = m
				}
			} else if m, ok := parsed["message"].(string); ok && m != "" {
				msg = m
			}
		}
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity {
			return http.StatusBadRequest, msg, false
		}
		log.Warn("media", "elevenlabs tts upstream error", "status", resp.StatusCode, "body", snippet)
		if h.Repo != nil {
			backoff := h.Repo.GetConnectionBackoffLevel(conn.ID)
			if classification := providers.ClassifyError(resp.StatusCode, snippet, backoff, respBody); classification.ShouldFallback {
				cooldownSec := max(classification.CooldownMs/1000, 1)
				if !handlerutil.IsProbeContext(r.Context()) {
					_ = h.Repo.LockConnectionModel(conn.ID, modelInfo.Model, cooldownSec, classification.NewBackoffLevel)
				}
				// done=false so the rotation loop records the failure and
				// surfaces it to the client once accounts are exhausted;
				// returning true here would answer with an empty 200.
				return resp.StatusCode, msg, false
			}
		}
		return resp.StatusCode, msg, false
	}

	if len(respBody) < 1024 {
		return http.StatusBadGateway, "ElevenLabs TTS returned empty audio", false
	}

	if h.Repo != nil {
		_ = h.Repo.UpdateConnectionLastUsed(conn.ID)
		if !handlerutil.IsProbeContext(r.Context()) {
			_ = h.Repo.UnlockConnectionModel(conn.ID, modelInfo.Model)
		}
	}
	h.trackTTSUsage(modelInfo.Provider, modelInfo.Model)
	h.writeTTSResponse(w, r, rf, respBody, out.mime, out.format)
	return 0, "", true
}

// elevenLabsTTSURL honors connection and registry base URL overrides the way
// the other elevenlabs lanes do, falling back to the vendor's default host.
// A connection baseUrl wins over the registry TTSURL: a custom node pointing
// at a proxy must serve TTS on the same host, not the vendor's.
func elevenLabsTTSURL(chatH *chat.ChatHandler, connData *chat.ConnectionData) string {
	if connData != nil && strings.TrimSpace(connData.BaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(connData.BaseURL), "/")
	}
	if cfg, err := chatH.GetProviderConfig("elevenlabs", connData); err == nil && cfg != nil && cfg.TTSURL != "" {
		return strings.TrimRight(cfg.TTSURL, "/")
	}
	return "https://api.elevenlabs.io/v1/text-to-speech"
}
