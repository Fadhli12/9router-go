package translator

import (
	"strings"
)

// SanitizeOrphanAssistantTurns removes orphan empty-assistant turns immediately
// followed by a tool-role message. An assistant turn is considered empty if it
// carries no text content (nil, empty string, or slice of content blocks with
// no text/whitespace-only), no reasoning content, and has no tool calls.
//
// When immediately followed by a message with Role == "tool", such empty assistant
// messages are invalid shells that confuse upstream provider translators and APIs
// (e.g. Claude, Gemini, Kiro).
//
// The returned slice is a new slice; messages that are kept preserve their
// content and order.
func SanitizeOrphanAssistantTurns(messages []OpenAIMessage) []OpenAIMessage {
	if len(messages) == 0 {
		return messages
	}

	out := make([]OpenAIMessage, 0, len(messages))
	for i, msg := range messages {
		// Only remove assistant message if it is empty AND immediately followed by role: tool.
		if msg.Role == "assistant" && isOpenAIAssistantEmpty(msg) && i+1 < len(messages) && messages[i+1].Role == "tool" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

// isOpenAIAssistantEmpty reports whether an assistant message carries no text,
// no reasoning, and no tool calls — i.e. a contentless shell.
func isOpenAIAssistantEmpty(msg OpenAIMessage) bool {
	if len(msg.ToolCalls) > 0 {
		return false
	}
	if strings.TrimSpace(msg.ReasoningContent) != "" {
		return false
	}
	return openAIContentEmpty(msg.Content)
}

// openAIContentEmpty reports whether an OpenAI message content carries no
// text. Content is a string, a []any of blocks (JSON-decoded), or a
// []OpenAIContentBlock (programmatically built).
func openAIContentEmpty(content any) bool {
	switch v := content.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, _ := m["text"].(string); strings.TrimSpace(text) != "" {
					return false
				}
			}
		}
		return true
	case []OpenAIContentBlock:
		for _, b := range v {
			if strings.TrimSpace(b.Text) != "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}
