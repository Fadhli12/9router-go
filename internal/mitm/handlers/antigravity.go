package handlers

import (
	json "encoding/json/v2"
	"net/http"
	"strings"

	"9router/proxy/internal/log"
	"9router/proxy/internal/providers"
)

const antigravityIDEVersion = "2.24.0"

// HandleAntigravity intercepts Antigravity Gemini-native requests and forwards to 9router.
// It preserves client identity for catalog requests (fetchAvailableModels) and only
// overrides IDE version for generation endpoints (:generateContent/:streamGenerateContent)
// to keep compatibility while letting the IDE see new models (#3414).
func HandleAntigravity(w http.ResponseWriter, r *http.Request, body []byte) {
	// Decide whether to override IDE version based on request URL
	requestURL := r.URL.String()
	isGeneration := strings.Contains(requestURL, ":generateContent") || strings.Contains(requestURL, ":streamGenerateContent")

	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		SendError(w, http.StatusBadRequest, "invalid Antigravity request body")
		return
	}

	// Recorded before the override below replaces it. `cl` comes from the
	// client's runtime config rather than its binary, so this interception is
	// the only place the genuine fingerprint is observable — and the gateway's
	// pinned identity can only be bumped from such a capture.
	//
	// Outside the isGeneration branch on purpose: the catalog lane keeps the
	// client's own identity, so it carries the same fingerprint and is the
	// cheaper request to capture from.
	logAntigravityClientIdentity(r, reqBody)

	// Apply IDE version override only for generation endpoints
	if isGeneration {
		// Override User-Agent header
		r.Header.Set("User-Agent", "antigravity/"+antigravityIDEVersion)
		// Override body metadata.ideVersion if present
		if meta, ok := reqBody["metadata"].(map[string]any); ok {
			meta["ideVersion"] = antigravityIDEVersion
			reqBody["metadata"] = meta
		} else if meta, ok := reqBody["request"].(map[string]any); ok {
			// Some payloads nest metadata inside request
			if innerMeta, ok := meta["metadata"].(map[string]any); ok {
				innerMeta["ideVersion"] = antigravityIDEVersion
			}
		}
	}

	model, _ := reqBody["model"].(string)
	if model != "" {
		reqBody["model"] = "antigravity/" + model
	}
	reqBody["userAgent"] = "antigravity"
	forwardBody, err := json.Marshal(reqBody)
	if err != nil {
		SendError(w, http.StatusInternalServerError, "failed to marshal Antigravity request")
		return
	}

	isStream := len(r.URL.Query().Get("alt")) > 0 || strings.Contains(requestURL, ":streamGenerateContent") || strings.Contains(requestURL, ":generateContent")

	upstream, err := FetchRouter(r.Context(), forwardBody, "/v1/chat/completions", r.Header, "")
	if err != nil {
		SendError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer upstream.Body.Close()

	if isStream || upstream.Header.Get("Content-Type") == "text/event-stream" {
		PipeSSE(upstream, w)
	} else {
		PipeJSON(upstream, w)
	}
}

// logAntigravityClientIdentity records the genuine client fingerprint from an
// intercepted request.
//
// Logged at warn level so it survives a default log configuration: a capture
// session is rare and deliberate, and an operator who ran one must not have to
// discover afterwards that the line was filtered out.
//
// The User-Agent and the body's ideVersion are logged separately because they
// disagree in practice — the CLI sends the aidev_client shape with the cl token
// while the IDE sends a bare version — and the gateway pins both.
func logAntigravityClientIdentity(r *http.Request, reqBody map[string]any) {
	ua := r.Header.Get("User-Agent")
	ideVersion := antigravityBodyIDEVersion(reqBody)
	if ua == "" && ideVersion == "" {
		return
	}
	log.Warn("mitm", "antigravity client identity observed",
		"user_agent", ua,
		"body_ide_version", ideVersion,
		"url", r.URL.String(),
		"pinned_cli", providers.AntigravityHubVersion,
		"pinned_ide", providers.AntigravityIDEVersion,
	)
}

// antigravityBodyIDEVersion reads metadata.ideVersion from either payload shape
// the clients send: top level, or nested under "request".
func antigravityBodyIDEVersion(reqBody map[string]any) string {
	if meta, ok := reqBody["metadata"].(map[string]any); ok {
		if v, ok := meta["ideVersion"].(string); ok && v != "" {
			return v
		}
	}
	if req, ok := reqBody["request"].(map[string]any); ok {
		if meta, ok := req["metadata"].(map[string]any); ok {
			if v, ok := meta["ideVersion"].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}
