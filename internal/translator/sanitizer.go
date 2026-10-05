package translator

import (
	"strings"
)

// SanitizeOpenAIMessages normalizes an OpenAI-format message list before it is
// handed to an upstream, cleaning two shapes that make providers reject an
// otherwise-valid multi-turn conversation:
//
//  1. Empty assistant shells. A turn that ended in tool calls often leaves a
//     trailing assistant message with no text and no tool_calls immediately
//     before the tool result(s). Some providers (Gemini, Anthropic) reject or
//     mis-handle a contentless assistant turn, so it is dropped.
//
//  2. Orphan tool messages. A tool message whose tool_call_id does not match
//     any preceding assistant tool_call (for example a background/system
//     notification injected with role "tool") is rewritten as contextual user
//     text "[Tool Result]: <content>". Upstreams otherwise try to pair it with
//     a nonexistent call and 400.
//
// The returned slice is a new slice; the input messages are not mutated in
// place where a message is dropped or rewritten.
func SanitizeOpenAIMessages(messages []OpenAIMessage) []OpenAIMessage {
	if len(messages) == 0 {
		return messages
	}

	// First pass: collect every tool_call id an assistant turn declared, so an
	// orphan tool message (one that references no such id) can be detected.
	knownToolCallIDs := make(map[string]struct{})
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				knownToolCallIDs[tc.ID] = struct{}{}
			}
		}
	}

	out := make([]OpenAIMessage, 0, len(messages))
	for i, msg := range messages {
		switch msg.Role {
		case "assistant":
			// Drop a run of consecutive empty assistant shells that leads
			// directly into a tool message (its own tool results).
			if isOpenAIAssistantEmpty(msg) {
				j := i + 1
				for j < len(messages) && messages[j].Role == "assistant" && isOpenAIAssistantEmpty(messages[j]) {
					j++
				}
				if j < len(messages) && messages[j].Role == "tool" {
					continue
				}
			}
			out = append(out, msg)

		case "tool":
			// A tool message with no matching preceding tool_call is orphaned:
			// rewrite it as user text so the upstream never tries to pair it.
			if msg.ToolCallID == "" {
				out = append(out, orphanToolToUser(msg))
				continue
			}
			if _, ok := knownToolCallIDs[msg.ToolCallID]; !ok {
				out = append(out, orphanToolToUser(msg))
				continue
			}
			out = append(out, msg)

		default:
			out = append(out, msg)
		}
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

// orphanToolToUser converts an orphaned tool message into a user message whose
// text is the tool's result, prefixed so a human (and the model) still sees it
// as a tool outcome rather than a free-form utterance.
func orphanToolToUser(msg OpenAIMessage) OpenAIMessage {
	text := extractContentString(msg.Content)
	return OpenAIMessage{
		Role:    "user",
		Content: "[Tool Result]: " + text,
	}
}
