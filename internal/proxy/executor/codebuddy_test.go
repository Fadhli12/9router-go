package executor

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"strings"
	"testing"
)

func TestTransformCodebuddyBody_ForceStream(t *testing.T) {
	// non-stream request should be forced to stream=true
	input := `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":false}`
	out, err := transformCodebuddyBody([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if m["stream"] != true {
		t.Errorf("expected stream=true, got %v", m["stream"])
	}
}

func TestTransformCodebuddyBody_StreamAlreadyTrue(t *testing.T) {
	input := `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":true}`
	out, err := transformCodebuddyBody([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if m["stream"] != true {
		t.Errorf("expected stream=true, got %v", m["stream"])
	}
}

func TestTransformCodebuddyBody_ReasoningEffortNone(t *testing.T) {
	// "none" and "off" should be stripped
	for _, eff := range []string{"none", "off"} {
		input := `{"model":"glm-5.2","messages":[],"reasoning_effort":"` + eff + `"}`
		out, err := transformCodebuddyBody([]byte(input))
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", eff, err)
		}
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		if _, exists := m["reasoning_effort"]; exists {
			t.Errorf("reasoning_effort=%q should be removed, but still present", eff)
		}
		if _, exists := m["reasoning_summary"]; exists {
			t.Errorf("reasoning_summary should NOT be set when effort=%q", eff)
		}
	}
}

func TestTransformCodebuddyBody_ReasoningEffortMedium(t *testing.T) {
	// Active reasoning_effort should inject reasoning_summary="auto"
	input := `{"model":"glm-5.2","messages":[],"reasoning_effort":"medium"}`
	out, err := transformCodebuddyBody([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if m["reasoning_effort"] != "medium" {
		t.Errorf("reasoning_effort should stay %q, got %v", "medium", m["reasoning_effort"])
	}
	if m["reasoning_summary"] != "auto" {
		t.Errorf("expected reasoning_summary=%q, got %v", "auto", m["reasoning_summary"])
	}
}

func TestTransformCodebuddyBody_NoReasoningEffort(t *testing.T) {
	// When reasoning_effort is not set, neither should reasoning_summary
	input := `{"model":"glm-5.2","messages":[{"role":"user","content":"hello"}]}`
	out, err := transformCodebuddyBody([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if _, exists := m["reasoning_effort"]; exists {
		t.Error("reasoning_effort should not be present")
	}
	if _, exists := m["reasoning_summary"]; exists {
		t.Error("reasoning_summary should not be present when no effort set")
	}
}

func TestTransformCodebuddyBody_UpstreamMessageShape(t *testing.T) {
	// Upstream codebuddy-intl.js parity: leading system prompt, user content
	// as typed blocks, client system messages dropped.
	input := `{"model":"glm-5.3-flash","messages":[{"role":"system","content":"client-sys"},{"role":"user","content":"hi"}],"stream":false}`
	out, err := transformCodebuddyBody([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m struct {
		Stream   bool `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !m.Stream {
		t.Error("stream must be forced true")
	}
	if len(m.Messages) != 2 {
		t.Fatalf("expected system + user messages, got %d: %s", len(m.Messages), out)
	}
	if m.Messages[0].Role != "system" {
		t.Errorf("first message must be system, got %q", m.Messages[0].Role)
	}
	blocks, ok := m.Messages[1].Content.([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("user content must be typed blocks, got %#v", m.Messages[1].Content)
	}
	block, _ := blocks[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "hi" {
		t.Errorf("unexpected user block: %#v", block)
	}
}

func TestSSEToOpenAIJSON_MergesChunks(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-1\",\"created\":123,\"model\":\"glm-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	out, ok := sseToOpenAIJSON([]byte(sse))
	if !ok {
		t.Fatal("expected SSE detected")
	}
	var resp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if resp.ID != "chatcmpl-1" || resp.Model != "glm-5.2" {
		t.Errorf("id/model mismatch: %+v", resp)
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("content not merged, got %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].Message.ReasoningContent != "thinking" {
		t.Errorf("reasoning_content missing, got %q", resp.Choices[0].Message.ReasoningContent)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason missing, got %q", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 5 {
		t.Errorf("usage not carried, got %+v", resp.Usage)
	}
}

func TestSSEToOpenAIJSON_MergesToolCalls(t *testing.T) {
	sse := "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\\\"KL\\\"}\"}}]}}]}\n\n"
	out, ok := sseToOpenAIJSON([]byte(sse))
	if !ok {
		t.Fatal("expected SSE detected")
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content   any `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if resp.Choices[0].Message.Content != nil {
		t.Errorf("content should be null when tool_calls present, got %v", resp.Choices[0].Message.Content)
	}
	tcs := resp.Choices[0].Message.ToolCalls
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].ID != "call_1" {
		t.Errorf("tool call id not carried, got %q", tcs[0].ID)
	}
	if tcs[0].Function.Name != "get_weather" {
		t.Errorf("tool name not merged, got %q", tcs[0].Function.Name)
	}
	if tcs[0].Function.Arguments != `{"city":"KL"}` {
		t.Errorf("tool arguments not merged, got %q", tcs[0].Function.Arguments)
	}
}

func TestSSEToOpenAIJSON_NotSSE(t *testing.T) {
	// A plain JSON error body must pass through untouched.
	if _, ok := sseToOpenAIJSON([]byte(`{"error":{"message":"boom","code":400}}`)); ok {
		t.Error("plain JSON should not be treated as SSE")
	}
}

func TestStripReasoningContentStream_RemovesReasoningChunks(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"kimi-k2.7\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"think \"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"more\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	filtered := stripReasoningContentStream(bytes.NewReader([]byte(sse)))
	out, err := io.ReadAll(filtered)
	if err != nil {
		t.Fatalf("read filtered stream: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "reasoning_content") {
		t.Errorf("filtered stream still contains reasoning_content:\n%s", got)
	}
	if !strings.Contains(got, `"content":"hello"`) {
		t.Errorf("filtered stream missing content chunk:\n%s", got)
	}
	if !strings.Contains(got, "[DONE]") {
		t.Errorf("filtered stream missing [DONE]:\n%s", got)
	}
}

func TestStripReasoningContentStream_PassesThroughNonReasoningChunks(t *testing.T) {
	filtered := stripReasoningContentStream(bytes.NewReader([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")))
	out, err := io.ReadAll(filtered)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if !strings.Contains(string(out), `"content":"ok"`) {
		t.Errorf("content chunk was dropped or altered:\n%s", out)
	}
}

// A long think arrives as a torrent of reasoning-only frames. Each one must be
// dropped whole, not re-marshalled as `"delta":{}`, or the client still eats
// thousands of empty frames — the terminal hang this executor exists to stop.
func TestStripReasoningContentStream_DropsEmptyReasoningFrames(t *testing.T) {
	sse := "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"b\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"c\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n"
	out, err := io.ReadAll(stripReasoningContentStream(bytes.NewReader([]byte(sse))))
	if err != nil {
		t.Fatalf("read filtered stream: %v", err)
	}
	frames := strings.Count(string(out), "data:")
	if frames != 2 {
		t.Fatalf("expected only the content frame and [DONE], got %d frames:\n%s", frames, out)
	}
	if strings.Contains(string(out), `"delta":{}`) {
		t.Errorf("empty delta frame leaked through:\n%s", out)
	}
}

func TestStripReasoningContentStream_KeepsToolCallsAndFinish(t *testing.T) {
	sse := "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"x\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"ls\",\"arguments\":\"{}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	out, err := io.ReadAll(stripReasoningContentStream(bytes.NewReader([]byte(sse))))
	if err != nil {
		t.Fatalf("read filtered stream: %v", err)
	}
	got := string(out)
	for _, want := range []string{"tool_calls", `"finish_reason":"tool_calls"`, "[DONE]"} {
		if !strings.Contains(got, want) {
			t.Errorf("filtered stream missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "reasoning_content") {
		t.Errorf("reasoning_content leaked:\n%s", got)
	}
}

func TestStripReasoningContentStream_TolerantDoneMatch(t *testing.T) {
	filtered := stripReasoningContentStream(bytes.NewReader([]byte("data: [DONE]   \n\n")))
	out, err := io.ReadAll(filtered)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if !strings.Contains(string(out), "[DONE]") {
		t.Errorf("padded [DONE] was not recognised:\n%s", out)
	}
}

// Vendors disagree on the thought-field name (translator/types.go OpenAIDelta:
// reasoning_content, reasoning, reasoning_details). An alt shape must be
// stripped too — and a frame left with only content must keep that content.
func TestStripReasoningContentStream_StripsAltReasoningShapes(t *testing.T) {
	sse := "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"thinking hard\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_details\":[{\"text\":\"step 1\"}]}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"hmm\",\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n"
	out, err := io.ReadAll(stripReasoningContentStream(bytes.NewReader([]byte(sse))))
	if err != nil {
		t.Fatalf("read filtered stream: %v", err)
	}
	got := string(out)
	for _, leaked := range []string{"thinking hard", "step 1", `"reasoning":`, "reasoning_details"} {
		if strings.Contains(got, leaked) {
			t.Errorf("alt reasoning shape leaked %q:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, `"content":"hi"`) {
		t.Errorf("mixed reasoning+content frame lost its content:\n%s", got)
	}
	if strings.Count(got, "data:") != 2 {
		t.Errorf("expected only the content frame and [DONE]:\n%s", got)
	}
}
