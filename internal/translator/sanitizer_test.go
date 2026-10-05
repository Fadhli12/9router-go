package translator

import (
	"reflect"
	"testing"
)

func TestSanitizeOpenAIMessages(t *testing.T) {
	tests := []struct {
		name     string
		input    []OpenAIMessage
		expected []OpenAIMessage
	}{
		{
			name:     "empty slice",
			input:    []OpenAIMessage{},
			expected: []OpenAIMessage{},
		},
		{
			name:     "nil slice",
			input:    nil,
			expected: nil,
		},
		{
			name: "empty-string content + no tool_calls + followed by tool -> removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "search something"},
				{Role: "assistant", Content: ""},
				{Role: "tool", ToolCallID: "call_1", Content: "search results"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "search something"},
				// Note: the tool message references call_1, which no assistant
				// declared, so it is also orphaned and rewritten to user text.
				{Role: "user", Content: "[Tool Result]: search results"},
			},
		},
		{
			name: "whitespace-only content + no tool_calls + followed by tool -> removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "search something"},
				{Role: "assistant", Content: "   \n\t  "},
				{Role: "tool", ToolCallID: "call_1", Content: "search results"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "search something"},
				{Role: "user", Content: "[Tool Result]: search results"},
			},
		},
		{
			name: "nil content + no tool_calls + followed by tool -> removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "run task"},
				{Role: "assistant", Content: nil},
				{Role: "tool", ToolCallID: "call_1", Content: "task output"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "run task"},
				{Role: "user", Content: "[Tool Result]: task output"},
			},
		},
		{
			name: "content-block slice with empty text + no tool_calls + followed by tool -> removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "do work"},
				{Role: "assistant", Content: []OpenAIContentBlock{{Type: "text", Text: "   "}}},
				{Role: "tool", ToolCallID: "call_1", Content: "done"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "do work"},
				{Role: "user", Content: "[Tool Result]: done"},
			},
		},
		{
			name: "JSON-decoded []any blocks with empty text + no tool_calls + followed by tool -> removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "query"},
				{Role: "assistant", Content: []any{map[string]any{"type": "text", "text": ""}}},
				{Role: "tool", ToolCallID: "call_1", Content: "result"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "query"},
				{Role: "user", Content: "[Tool Result]: result"},
			},
		},
		{
			name: "non-empty text content + followed by tool -> NOT removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "calculate 2+2"},
				{Role: "assistant", Content: "Let me compute that."},
				{Role: "tool", ToolCallID: "call_1", Content: "4"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "calculate 2+2"},
				{Role: "assistant", Content: "Let me compute that."},
				{Role: "user", Content: "[Tool Result]: 4"},
			},
		},
		{
			name: "assistant with tool_calls + followed by tool -> kept",
			input: []OpenAIMessage{
				{Role: "user", Content: "what is the weather"},
				{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_1", Function: OpenAIFunctionCall{Name: "get_weather", Arguments: "{}"}}}},
				{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "what is the weather"},
				{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_1", Function: OpenAIFunctionCall{Name: "get_weather", Arguments: "{}"}}}},
				{Role: "tool", ToolCallID: "call_1", Content: "sunny"},
			},
		},
		{
			name: "empty assistant turn is last message -> NOT removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: ""},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: ""},
			},
		},
		{
			name: "multiple consecutive orphan assistant turns before tool -> all removed",
			input: []OpenAIMessage{
				{Role: "user", Content: "run check"},
				{Role: "assistant", Content: ""},
				{Role: "assistant", Content: nil},
				{Role: "tool", ToolCallID: "call_1", Content: "all checks pass"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "run check"},
				{Role: "user", Content: "[Tool Result]: all checks pass"},
			},
		},
		{
			name: "orphan tool message without matching call -> rewritten to user text",
			input: []OpenAIMessage{
				{Role: "user", Content: "run something"},
				{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_1", Function: OpenAIFunctionCall{Name: "bash", Arguments: "{}"}}}},
				{Role: "tool", ToolCallID: "call_1", Content: "ok"},
				{Role: "tool", ToolCallID: "call_999", Content: "background notification"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "run something"},
				{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_1", Function: OpenAIFunctionCall{Name: "bash", Arguments: "{}"}}}},
				{Role: "tool", ToolCallID: "call_1", Content: "ok"},
				{Role: "user", Content: "[Tool Result]: background notification"},
			},
		},
		{
			name: "tool message with no tool_call_id -> rewritten to user text",
			input: []OpenAIMessage{
				{Role: "user", Content: "hi"},
				{Role: "tool", Content: "injected result"},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "hi"},
				{Role: "user", Content: "[Tool Result]: injected result"},
			},
		},
		{
			name: "normal user -> assistant(with tool_calls) -> tool turn preserved unchanged",
			input: []OpenAIMessage{
				{Role: "user", Content: "sum 1 and 2"},
				{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_a", Function: OpenAIFunctionCall{Name: "add", Arguments: "{\"a\":1,\"b\":2}"}}}},
				{Role: "tool", ToolCallID: "call_a", Content: "3"},
				{Role: "assistant", Content: "The sum is 3."},
			},
			expected: []OpenAIMessage{
				{Role: "user", Content: "sum 1 and 2"},
				{Role: "assistant", Content: "", ToolCalls: []OpenAIToolCall{{ID: "call_a", Function: OpenAIFunctionCall{Name: "add", Arguments: "{\"a\":1,\"b\":2}"}}}},
				{Role: "tool", ToolCallID: "call_a", Content: "3"},
				{Role: "assistant", Content: "The sum is 3."},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeOpenAIMessages(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("SanitizeOpenAIMessages() = %+v, want %+v", got, tt.expected)
			}
		})
	}
}

// TestSanitizeOpenAIMessages_DoesNotMutateInput guards against in-place
// mutation: the returned slice must be a distinct slice even when nothing is
// dropped or rewritten.
func TestSanitizeOpenAIMessages_DoesNotMutateInput(t *testing.T) {
	in := []OpenAIMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	out := SanitizeOpenAIMessages(in)
	if &out[0] == &in[0] {
		t.Errorf("expected a distinct backing slice, got the same one")
	}
}
