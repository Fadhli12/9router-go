package executor

import (
	"net/http/httptest"
	"strings"
	"testing"

	json "encoding/json/v2"
)

// jsonResponse now folds an event stream into one chat.completion, so the
// risk this test guards is a false positive: an ordinary JSON body must reach
// the client byte-for-byte. Every other provider on this path (openai, iflow,
// kimchi, commandcode, opencode) answers with plain JSON.
func TestJSONResponse_LeavesPlainJSONUntouched(t *testing.T) {
	const upstream = `{"id":"chatcmpl-7","object":"chat.completion","created":9,` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"plain"},"finish_reason":"stop"}]}`

	rec := httptest.NewRecorder()
	if err := jsonResponse(t.Context(), rec, strings.NewReader(upstream), false, nil); err != nil {
		t.Fatalf("jsonResponse: %v", err)
	}
	if got := rec.Body.String(); got != upstream {
		t.Errorf("body was rewritten.\n got: %s\nwant: %s", got, upstream)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// An error envelope from a provider that does not speak SSE must pass through
// as well, and must not be mistaken for a stream.
func TestJSONResponse_LeavesErrorJSONUntouched(t *testing.T) {
	const upstream = `{"error":{"message":"model not found","type":"invalid_request_error","code":404}}`

	rec := httptest.NewRecorder()
	if err := jsonResponse(t.Context(), rec, strings.NewReader(upstream), false, nil); err != nil {
		t.Fatalf("jsonResponse: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("error body is not JSON: %v", err)
	}
	if _, ok := out["error"]; !ok {
		t.Errorf("error envelope was lost: %s", rec.Body.String())
	}
}
