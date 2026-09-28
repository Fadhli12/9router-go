package chat

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"9router/proxy/internal/db"
)

const responsesRequestBody = `{"model":"%s","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true}`

// upstreamCapture records what a provider actually received so a test can
// assert the shape of the request, not just the shape of the reply.
type upstreamCapture struct {
	mu   sync.Mutex
	body string
}

func (c *upstreamCapture) got() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

func (c *upstreamCapture) record(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = string(b)
}

// TestHandleResponses_ChatUpstreamIsBridged is the end-to-end contract for a
// /v1/responses client talking to a Chat Completions provider.
func TestHandleResponses_ChatUpstreamIsBridged(t *testing.T) {
	capture := &upstreamCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		capture.record(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"pong\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	seedConnDB(t, database, "bn", "conn-responses-bridge", "sk-bridge", upstream.URL)
	handler := NewChatHandler(db.NewRepo(database))

	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader([]byte(fmt.Sprintf(responsesRequestBody, "bn/claude-sonnet-4.5"))))
	rec := httptest.NewRecorder()
	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	sent := capture.got()
	if !strings.Contains(sent, `"messages"`) {
		t.Errorf("upstream never received a Chat Completions body: %s", sent)
	}
	if strings.Contains(sent, `"input"`) {
		t.Errorf("Responses input[] leaked to a chat upstream: %s", sent)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "event: response.output_text.delta") || !strings.Contains(out, "pong") {
		t.Errorf("client did not receive Responses text events:\n%s", out)
	}
	if !strings.Contains(out, "event: response.completed") {
		t.Errorf("stream was never closed with response.completed:\n%s", out)
	}
}

// TestHandleResponses_NativeUpstreamIsNotTranslated is the guard on the other
// side: a Responses-native endpoint must keep the request it was given, because
// converting it to Chat Completions silently drops previous_response_id and
// item ids, and codex loses its server-side conversation.
func TestHandleResponses_NativeUpstreamIsNotTranslated(t *testing.T) {
	capture := &upstreamCapture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		capture.record(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n"))
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))

	}))
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	seedConnDB(t, database, "codex", "conn-responses-native", "sk-codex", upstream.URL+"/responses")
	handler := NewChatHandler(db.NewRepo(database))

	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader([]byte(fmt.Sprintf(responsesRequestBody, "codex/gpt-5.1"))))
	rec := httptest.NewRecorder()
	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	sent := capture.got()
	if !strings.Contains(sent, `"input"`) {
		t.Errorf("native upstream lost its input[] payload: %s", sent)
	}
	if strings.Contains(sent, `"messages"`) {
		t.Errorf("request was translated for a Responses-native upstream: %s", sent)
	}
	if !strings.Contains(rec.Body.String(), "response.output_text.delta") {
		t.Errorf("native events were not relayed:\n%s", rec.Body.String())
	}
}

// TestHandleResponses_NonStreamingAnswersWithResponseObject is the same contract
// for a client that did not ask to stream: one Response object, not a Chat
// Completions body the client cannot read.
func TestHandleResponses_NonStreamingAnswersWithResponseObject(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":4}}`))
	}))
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	seedConnDB(t, database, "bn", "conn-responses-json", "sk-json", upstream.URL)
	handler := NewChatHandler(db.NewRepo(database))

	req := httptest.NewRequest("POST", "/v1/responses", bytes.NewReader([]byte(`{"model":"bn/fake-model","input":"ping"}`)))
	rec := httptest.NewRecorder()
	handler.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if strings.Contains(out, "chat.completion") {
		t.Errorf("Chat Completions body reached a Responses client:\n%s", out)
	}
	for _, want := range []string{`"object":"response"`, `"status":"completed"`, "pong"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in the response body:\n%s", want, out)
		}
	}
}
