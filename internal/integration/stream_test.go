//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// TestStreamCompletionRelaysEvents pins the streaming contract end to end: the
// client asks for SSE, the gateway asks the upstream for SSE, the events reach
// the client in order, and the stream is terminated with [DONE]. A client that
// never sees [DONE] waits until its own timeout, which is the single most
// common "the gateway hangs" report.
func TestStreamCompletionRelaysEvents(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, streamResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", true))
	if res.Status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	if got := res.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	payloads := sseDataLines(t, res.Body)
	if len(payloads) == 0 {
		t.Fatal("stream carried no data frames")
	}
	if last := payloads[len(payloads)-1]; last != "[DONE]" {
		t.Errorf("last SSE frame = %q, want the [DONE] sentinel", last)
	}
	if !strings.Contains(string(res.Body), "streamed ") {
		t.Errorf("stream body missing the upstream content delta: %q", truncate(res.Body))
	}

	sent := upstream.Last(t)
	if got := sent.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("upstream Accept = %q, want text/event-stream for a streaming request", got)
	}
	if got := sent.Model(t); got != "deepseek-chat" {
		t.Errorf("upstream model = %q, want \"deepseek-chat\"", got)
	}
}

// TestStreamPassesThroughProviderErrorEnvelope pins that an upstream that fails
// before the first byte still yields a well-formed OpenAI error to the client
// instead of an empty 200 or a bare HTML error page — SSE clients parse the
// body as JSON events and would report a confusing parse failure.
func TestStreamPassesThroughProviderErrorEnvelope(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, JSONResponder(http.StatusTooManyRequests,
		`{"error":{"message":"slow down","type":"rate_limit_error","code":"rate_limit_exceeded"}}`))
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", true))
	if res.Status != http.StatusTooManyRequests {
		t.Fatalf("POST /v1/chat/completions = %d, want 429 (body: %s)", res.Status, truncate(res.Body))
	}
	if msg := res.ErrorMessage(t); !strings.Contains(msg, "slow down") {
		t.Errorf("error message = %q, want the upstream reason", msg)
	}
}

// sseDataLines extracts the payload of every "data:" frame in an SSE body.
func sseDataLines(t *testing.T, body []byte) []string {
	t.Helper()
	var payloads []string
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		payloads = append(payloads, payload)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan SSE body: %v", err)
	}
	return payloads
}
