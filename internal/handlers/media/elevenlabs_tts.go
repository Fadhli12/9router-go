package media

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
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
// (/v1/text-to-speech/{voice_id}) and the body is {text, model_id, ...},
// so the generic media funnel would post an OpenAI body to a path ElevenLabs
// never serves (404 {"detail":"Not Found"}).
//
// Model wire convention (same as upstream): the resolved model carries
// "model_id/voice_id" (e.g. el/eleven_multilingual_v2/<voice_id>), a model id
// paired with the OpenAI `voice` field (el/eleven_v4 + voice), or a bare
// voice_id (defaults to eleven_flash_v2_5).

const elevenLabsDefaultTTSModel = "eleven_flash_v2_5"

func (h *MediaHandler) forwardElevenLabsTTS(w http.ResponseWriter, r *http.Request, body []byte, modelInfo *chat.ModelInfo, input, voice, respFmt string) {
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)

	preferredConnID := preferredMediaConnID(r, modelInfo)
	// Upstream parity (tts.js credential loop): rotate accounts, lock failures.
	usePinned := preferredConnID != ""
	excludeIDs := []string{}
	tried := make(map[string]struct{})
	var lastErr *mediaForwardAttemptErr

	for {
		if r.Context().Err() != nil {
			if lastErr != nil {
				lastErr.write(w)
				return
			}
			handlerutil.WriteJSONError(w, http.StatusBadGateway, r.Context().Err().Error())
			return
		}
		conn, connData, err := h.ChatH.GetBestConnectionWithContext(r.Context(), "elevenlabs", preferredConnID, excludeIDs, modelInfo.Model)
		if err != nil || conn == nil || connData == nil {
			if lastErr != nil {
				lastErr.write(w)
				return
			}
			handlerutil.WriteJSONError(w, http.StatusNotFound, "no active connections for provider: elevenlabs")
			return
		}
		if _, seen := tried[conn.ID]; seen {
			if lastErr != nil {
				lastErr.write(w)
				return
			}
			handlerutil.WriteJSONError(w, http.StatusNotFound, "no active connections for provider: elevenlabs")
			return
		}
		tried[conn.ID] = struct{}{}

		attemptErr := h.tryElevenLabsTTSConn(w, r, modelInfo, input, voice, respFmt, raw, conn, connData)
		if attemptErr == nil {
			return
		}
		lastErr = attemptErr
		if usePinned || !attemptErr.retryable || r.Context().Err() != nil {
			lastErr.write(w)
			return
		}
		excludeIDs = append(excludeIDs, conn.ID)
	}
}

// parseElevenLabsModelVoice splits the resolved model into (model_id, voice_id),
// mirroring upstream's adapter: explicit "model/voice" takes precedence, then
// a non-default OpenAI `voice` parameter pairs with the resolved model, and a
// bare value is treated as the voice_id with eleven_flash_v2_5.
func parseElevenLabsModelVoice(resolvedModel, voiceParam string) (modelID, voiceID string) {
	trimmed := strings.TrimSpace(resolvedModel)
	if m, v, found := strings.Cut(trimmed, "/"); found && strings.TrimSpace(v) != "" {
		modelPart := strings.TrimSpace(m)
		if modelPart == "" {
			modelPart = elevenLabsDefaultTTSModel
		}
		return modelPart, strings.TrimSpace(v)
	}
	trimmed = strings.TrimSuffix(trimmed, "/")
	if vp := strings.TrimSpace(voiceParam); vp != "" && vp != "alloy" && vp != "default" {
		if trimmed == "" {
			return elevenLabsDefaultTTSModel, vp
		}
		return trimmed, vp
	}
	return elevenLabsDefaultTTSModel, trimmed
}

type elevenLabsOutputSpec struct {
	param, format, mime string
}

var elevenLabsOutputSpecs = map[string]elevenLabsOutputSpec{
	"opus": {"opus_48000_128", "opus", "audio/opus"},
	"wav":  {"wav_24000", "wav", "audio/wav"},
	"pcm":  {"pcm_24000", "pcm", "audio/pcm"},
}

func elevenLabsOutputFormat(respFmt string) elevenLabsOutputSpec {
	if spec, ok := elevenLabsOutputSpecs[strings.ToLower(strings.TrimSpace(respFmt))]; ok {
		return spec
	}
	return elevenLabsOutputSpec{"mp3_44100_128", "mp3", "audio/mpeg"}
}

func buildElevenLabsTTSPayload(input, modelID string, raw map[string]any) ([]byte, error) {
	payload := map[string]any{"text": input, "model_id": modelID}
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
	return json.Marshal(payload)
}

func extractElevenLabsErrorMessage(respBody []byte, status string) string {
	snippet := strings.TrimSpace(string(respBody[:min(300, len(respBody))]))
	if snippet == "" {
		return status
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err == nil {
		if d, ok := parsed["detail"].(map[string]any); ok {
			if m, ok := d["message"].(string); ok && m != "" {
				return m
			}
		} else if m, ok := parsed["message"].(string); ok && m != "" {
			return m
		}
	}
	return snippet
}

func (h *MediaHandler) tryElevenLabsTTSConn(
	w http.ResponseWriter, r *http.Request, modelInfo *chat.ModelInfo,
	input, voice, respFmt string, raw map[string]any,
	conn *models.ProviderConnection, connData *chat.ConnectionData,
) *mediaForwardAttemptErr {
	apiKey := chat.ExtractAPIKey(connData)
	if apiKey == "" {
		return &mediaForwardAttemptErr{status: http.StatusUnauthorized, jsonMsg: "no API key found for elevenlabs", retryable: true}
	}
	modelID, voiceID := parseElevenLabsModelVoice(modelInfo.Model, voice)
	if voiceID == "" {
		return &mediaForwardAttemptErr{status: http.StatusBadRequest, jsonMsg: "missing voice id for elevenlabs TTS", retryable: false}
	}
	providerCfg, err := h.ChatH.GetProviderConfig("elevenlabs", connData)
	if err != nil {
		return &mediaForwardAttemptErr{status: http.StatusInternalServerError, jsonMsg: "get elevenlabs config: " + err.Error(), retryable: false}
	}
	payloadBytes, err := buildElevenLabsTTSPayload(input, modelID, raw)
	if err != nil {
		return &mediaForwardAttemptErr{status: http.StatusInternalServerError, jsonMsg: "build elevenlabs tts request: " + err.Error(), retryable: false}
	}

	out := elevenLabsOutputFormat(respFmt)
	ttsURL := elevenLabsTTSURL(connData, providerCfg) + "/" + url.PathEscape(voiceID) + "?output_format=" + url.QueryEscape(out.param)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, ttsURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return &mediaForwardAttemptErr{status: http.StatusInternalServerError, jsonMsg: "create elevenlabs tts request: " + err.Error(), retryable: false}
	}
	req.Header.Set("Content-Type", "application/json")
	authHeader, authScheme := "xi-api-key", "raw"
	if providerCfg != nil {
		if providerCfg.AuthHeader != "" {
			authHeader = providerCfg.AuthHeader
		}
		if providerCfg.AuthScheme != "" {
			authScheme = providerCfg.AuthScheme
		}
	}
	handlerutil.SetAuthHeader(req, apiKey, authHeader, authScheme)
	if providerCfg != nil {
		for k, v := range providerCfg.StaticHeaders {
			if req.Header.Get(k) == "" {
				req.Header.Set(k, v)
			}
		}
	}

	client, clientErr := h.ChatH.GetClientForConnection(connData)
	if clientErr != nil {
		return &mediaForwardAttemptErr{status: http.StatusBadGateway, jsonMsg: clientErr.Error(), retryable: true}
	}
	if client == nil {
		client = h.Client
	}
	resp, err := guardMediaRedirects(client).Do(req)
	if err != nil {
		return &mediaForwardAttemptErr{status: http.StatusBadGateway, jsonMsg: "elevenlabs tts request failed: " + err.Error(), retryable: r.Context().Err() == nil}
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return &mediaForwardAttemptErr{status: http.StatusBadGateway, jsonMsg: "read elevenlabs response: " + err.Error(), retryable: r.Context().Err() == nil}
	}
	if resp.StatusCode != http.StatusOK {
		msg := extractElevenLabsErrorMessage(respBody, resp.Status)
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity {
			return &mediaForwardAttemptErr{status: http.StatusBadRequest, jsonMsg: msg, retryable: false}
		}
		snippet := strings.TrimSpace(string(respBody[:min(300, len(respBody))]))
		log.Warn("media", "elevenlabs tts upstream error", "status", resp.StatusCode, "body", snippet)
		retryable := false
		if h.Repo != nil {
			backoff := h.Repo.GetConnectionBackoffLevel(r.Context(), conn.ID)
			if classification := providers.ClassifyError(resp.StatusCode, snippet, backoff, respBody); classification.ShouldFallback {
				cooldownSec := max(classification.CooldownMs/1000, 1)
				if !handlerutil.IsProbeContext(r.Context()) {
					_ = h.Repo.LockConnectionModel(r.Context(), conn.ID, modelInfo.Model, cooldownSec, classification.NewBackoffLevel)
				}
				retryable = true
			}
		}
		return &mediaForwardAttemptErr{status: resp.StatusCode, jsonMsg: fmt.Sprintf("elevenlabs tts failed with status %d: %s", resp.StatusCode, msg), retryable: retryable}
	}
	if len(respBody) < 1024 {
		return &mediaForwardAttemptErr{status: http.StatusBadGateway, jsonMsg: "ElevenLabs TTS returned empty audio", retryable: true}
	}

	if h.Repo != nil {
		_ = h.Repo.UpdateConnectionLastUsed(conn.ID)
		if !handlerutil.IsProbeContext(r.Context()) {
			_ = h.Repo.UnlockConnectionModel(r.Context(), conn.ID, modelInfo.Model)
		}
	}
	h.trackTTSUsage("elevenlabs", modelInfo.Model)
	wtFmt := respFmt
	if wtFmt == "" {
		wtFmt = "mp3"
	}
	h.writeTTSResponse(w, r, wtFmt, respBody, out.mime, out.format)
	return nil
}

// elevenLabsTTSURL honors per-connection and registry base URL overrides,
// falling back to the vendor's default host.
func elevenLabsTTSURL(connData *chat.ConnectionData, providerCfg *providers.ProviderConfig) string {
	base := "https://api.elevenlabs.io/v1/text-to-speech"
	switch {
	case connData != nil && strings.TrimSpace(connData.BaseURL) != "":
		base = strings.TrimRight(strings.TrimSpace(connData.BaseURL), "/")
	case providerCfg != nil && strings.TrimSpace(providerCfg.TTSURL) != "":
		base = strings.TrimRight(strings.TrimSpace(providerCfg.TTSURL), "/")
	}
	if !strings.Contains(base, "text-to-speech") {
		base += "/v1/text-to-speech"
	}
	return base
}
