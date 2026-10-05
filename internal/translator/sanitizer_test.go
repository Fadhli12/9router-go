package translator

import (
	"testing"
)

// TestSanitizeOpenAIMessages_EmptyAssistantBeforeTool drops an empty assistant
// shell that directly precedes a tool message.
func TestSanitizeOpenAIMessages_EmptyAssistantBeforeTool(t *testing.T) {
	in := []OpenAIMessage{
		{Role: "user", Content: "look up the weather"},
		{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_1", Function: OpenAIFunctionCall{Name: "get_weather", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
		// Empty shell immediately before a tool message: must be dropped.
		{Role: "assistant", Content: ""},
		{Role: "tool", ToolCallID: "call_1", Content: "still sunny"},
	}

	out := SanitizeOpenAIMessages(in)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages after dropping empty assistant, got %d", len(out))
	}
	if out[3].Role != "tool" {
		t.Errorf("expected index 3 to be a tool message, got role %q", out[3].Role)
	}
}

// TestSanitizeOpenAIMessages_EmptyAssistantNotBeforeTool keeps an empty
// assistant shell when it is not directly followed by a tool message.
func TestSanitizeOpenAIMessages_EmptyAssistantNotBeforeTool(t *testing.T) {
	in := []OpenAIMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
		{Role: "user", Content: "are you there?"},
	}

	out := SanitizeOpenAIMessages(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if out[1].Role != "assistant" {
		t.Errorf("expected index 1 to remain an assistant message, got %q", out[1].Role)
	}
}

// TestSanitizeOpenAIMessages_NonEmptyAssistantKept verifies an assistant turn
// with text or reasoning is never dropped.
func TestSanitizeOpenAIMessages_NonEmptyAssistantKept(t *testing.T) {
	in := []OpenAIMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello!"},
		{Role: "tool", ToolCallID: "call_1", Content: "x"},
		{Role: "assistant", ReasoningContent: "thinking..."},
		{Role: "tool", ToolCallID: "call_1", Content: "y"},
	}

	out := SanitizeOpenAIMessages(in)
	if len(out) != 5 {
		t.Fatalf("expected 5 messages (none dropped), got %d", len(out))
	}
}

// TestSanitizeOpenAIMessages_OrphanToolRewritten verifies a tool message whose
// tool_call_id matches no assistant tool_call becomes user text.
func TestSanitizeOpenAIMessages_OrphanToolRewritten(t *testing.T) {
	in := []OpenAIMessage{
		{Role: "user", Content: "run something"},
		{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_1", Function: OpenAIFunctionCall{Name: "bash", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
		// Orphan: no assistant ever declared "call_999".
		{Role: "tool", ToolCallID: "call_999", Content: "background notification"},
	}

	out := SanitizeOpenAIMessages(in)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(out))
	}
	last := out[3]
	if last.Role != "user" {
		t.Fatalf("expected orphan tool to become user, got role %q", last.Role)
	}
	if last.Content != "[Tool Result]: background notification" {
		t.Errorf("unexpected orphan content %q", last.Content)
	}
}

// TestSanitizeOpenAIMessages_OrphanToolNoID verifies a tool message with no
// tool_call_id at all is treated as orphaned.
func TestSanitizeOpenAIMessages_OrphanToolNoID(t *testing.T) {
	in := []OpenAIMessage{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: "injected result"},
	}

	out := SanitizeOpenAIMessages(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out))
	}
	if out[1].Role != "user" {
		t.Errorf("expected user role, got %q", out[1].Role)
	}
	if out[1].Content != "[Tool Result]: injected result" {
		t.Errorf("unexpected content %q", out[1].Content)
	}
}

// TestSanitizeOpenAIMessages_EmptyList is a no-op on an empty list.
func TestSanitizeOpenAIMessages_EmptyList(t *testing.T) {
	out := SanitizeOpenAIMessages(nil)
	if len(out) != 0 {
		t.Errorf("expected empty output, got %d", len(out))
	}
}

// TestSanitizeOpenAIMessages_ArrayContentEmptyDetection verifies empty-content
// detection works for the []any (JSON-decoded block) form.
func TestSanitizeOpenAIMessages_ArrayContentEmptyDetection(t *testing.T) {
	in := []OpenAIMessage{
		{Role: "user", Content: "hi"},
		// Empty []any block is a shell; the orphan tool below is also rewritten.
		{Role: "assistant", Content: []any{map[string]any{"type": "text", "text": ""}}},
		{Role: "tool", ToolCallID: "call_1", Content: "x"},
	}

	out := SanitizeOpenAIMessages(in)
	if len(out) != 2 {
		t.Fatalf("expected empty-array assistant dropped, got %d messages", len(out))
	}
	if out[1].Role != "user" {
		t.Errorf("expected orphan tool rewritten to user at index 1, got %q", out[1].Role)
	}
}
