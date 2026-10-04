package chat

import (
	json "encoding/json/v2"
	"fmt"
)

// repairToolCallIDsInMap ensures every tool invocation (OpenAI tool_calls, Anthropic tool_use)
// and corresponding result (OpenAI role: "tool", Anthropic tool_result) has a valid, non-empty ID (PR #4090).
// Strict upstreams (Vertex AI Anthropic, Antigravity, NVIDIA, OpenAI) reject requests with HTTP 400
// (e.g. "messages.1.content.0.tool_use.id: Field required") when tool ID is missing or empty.
func repairToolCallIDsInMap(body map[string]any) {
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return
	}
	var pendingToolCallIDs []string
	toolSeq := 0
	for i, m := range msgs {
		msgMap, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msgMap["role"].(string)

		// 1. OpenAI assistant tool_calls
		if role == "assistant" {
			if tcs, ok := msgMap["tool_calls"].([]any); ok && len(tcs) > 0 {
				for k, tcRaw := range tcs {
					if tcMap, ok := tcRaw.(map[string]any); ok {
						id, _ := tcMap["id"].(string)
						if id == "" {
							id = fmt.Sprintf("call_%d_%d", toolSeq, k)
							tcMap["id"] = id
						}
						pendingToolCallIDs = append(pendingToolCallIDs, id)
					}
				}
				toolSeq++
			}

			// 2. Anthropic assistant content blocks with type "tool_use"
			if contentBlocks, ok := msgMap["content"].([]any); ok {
				for k, blockRaw := range contentBlocks {
					if blockMap, ok := blockRaw.(map[string]any); ok {
						bType, _ := blockMap["type"].(string)
						if bType == "tool_use" {
							id, _ := blockMap["id"].(string)
							if id == "" {
								id = fmt.Sprintf("toolu_%d_%d", toolSeq, k)
								blockMap["id"] = id
							}
							pendingToolCallIDs = append(pendingToolCallIDs, id)
						}
					}
				}
				toolSeq++
			}
		} else if role == "tool" {
			// 3. OpenAI tool result message
			id, _ := msgMap["tool_call_id"].(string)
			if id != "" {
				for idx, p := range pendingToolCallIDs {
					if p == id {
						pendingToolCallIDs = append(pendingToolCallIDs[:idx], pendingToolCallIDs[idx+1:]...)
						break
					}
				}
			} else {
				if len(pendingToolCallIDs) > 0 {
					msgMap["tool_call_id"] = pendingToolCallIDs[0]
					pendingToolCallIDs = pendingToolCallIDs[1:]
				} else {
					msgMap["tool_call_id"] = fmt.Sprintf("call_tool_%d", i)
				}
			}
		} else if role == "user" {
			// 4. Anthropic tool_result content blocks inside user messages
			if contentBlocks, ok := msgMap["content"].([]any); ok {
				for _, blockRaw := range contentBlocks {
					if blockMap, ok := blockRaw.(map[string]any); ok {
						bType, _ := blockMap["type"].(string)
						if bType == "tool_result" {
							id, _ := blockMap["tool_use_id"].(string)
							if id != "" {
								for idx, p := range pendingToolCallIDs {
									if p == id {
										pendingToolCallIDs = append(pendingToolCallIDs[:idx], pendingToolCallIDs[idx+1:]...)
										break
									}
								}
							} else {
								if len(pendingToolCallIDs) > 0 {
									blockMap["tool_use_id"] = pendingToolCallIDs[0]
									pendingToolCallIDs = pendingToolCallIDs[1:]
								} else {
									blockMap["tool_use_id"] = fmt.Sprintf("toolu_res_%d", i)
								}
							}
						}
					}
				}
			}
		}
	}
}

// repairToolCallIDsInJSON repairs missing tool IDs directly on raw JSON body if it contains messages.
func repairToolCallIDsInJSON(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	if _, ok := m["messages"]; ok {
		repairToolCallIDsInMap(m)
		if out, err := json.Marshal(m); err == nil {
			return out
		}
	}
	return body
}
