package translator

import (
	"fmt"
	"strings"
)

// canonicalizeKiroConversation reconciles assistant toolUses with the
// toolResults in the immediately following user turn. Kiro rejects a
// conversation where an assistant toolUse has no matching toolResult, or
// where a toolResult references a toolUseId that was never emitted. The
// fix flattens unmatched material into plain text so the envelope is valid.
//
// Ported from open-sse/translator/concerns/kiroConversation.js
// canonicalizeKiroConversation + reconcileToolPair.
func canonicalizeKiroConversation(history []map[string]any, current map[string]any, model string, specs []any, nameMap map[string]string) ([]map[string]any, map[string]any) {
	if len(history) == 0 {
		return history, current
	}

	// Build a set of spec names for quick lookup.
	specNames := make(map[string]bool, len(specs))
	for _, s := range specs {
		if spec, ok := s.(map[string]any); ok {
			if ts, ok := spec["toolSpecification"].(map[string]any); ok {
				if n, ok := ts["name"].(string); ok && n != "" {
					specNames[n] = true
				}
			}
		}
	}

	// Combine history + current into a single slice so reconcileToolPair can
	// look ahead across the history/current boundary. The last element is
	// always the current user turn.
	turns := make([]map[string]any, 0, len(history)+1)
	turns = append(turns, history...)
	// Wrap current back into a turn envelope so the loop sees a uniform shape.
	currentTurn := map[string]any{"userInputMessage": current}
	turns = append(turns, currentTurn)

	usedIds := make(map[string]bool)

	for i := 0; i < len(turns); i++ {
		turn := turns[i]
		if arm, ok := turn["assistantResponseMessage"].(map[string]any); ok {
			toolUses, _ := arm["toolUses"].([]any)
			if len(toolUses) > 0 {
				// Look ahead to the next user turn for toolResults.
				var nextUser map[string]any
				if i+1 < len(turns) {
					if uim, ok := turns[i+1]["userInputMessage"].(map[string]any); ok {
						nextUser = uim
					}
				}
				reconcileKiroToolPair(arm, nextUser, specNames, usedIds)
			}
		}
	}

	// Rebuild history from the reconciled turns (all but the last = current).
	outHistory := make([]map[string]any, 0, len(turns)-1)
	for i := 0; i < len(turns)-1; i++ {
		turn := turns[i]
		// Skip empty assistant turns that were left behind.
		if arm, ok := turn["assistantResponseMessage"].(map[string]any); ok {
			content, _ := arm["content"].(string)
			uses, _ := arm["toolUses"].([]any)
			if strings.TrimSpace(content) == "" && len(uses) == 0 {
				continue
			}
		}
		outHistory = append(outHistory, turn)
	}

	// The current user turn must not carry toolResults that reference IDs
	// already consumed by history (or IDs that were never emitted at all).
	// Exception: a tool result that was legitimately paired with a history
	// assistant toolUse by reconcileKiroToolPair must stay — it is the
	// matching half of that pair.
	if uim, ok := current["userInputMessage"].(map[string]any); ok {
		if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
			if results, _ := ctx["toolResults"].([]any); len(results) > 0 {
				var kept []any
				for _, r := range results {
					rm, ok := r.(map[string]any)
					if !ok {
						continue
					}
					id, _ := rm["toolUseId"].(string)
					if id == "" {
						continue
					}
					// If the ID was consumed by a history assistant toolUse
					// (meaning reconcileKiroToolPair matched it), the result
					// must stay in the current turn as the matching half.
					if usedIds[id] {
						kept = append(kept, rm)
						continue
					}
					// ID was never emitted by any assistant turn — orphan.
					// Flatten it into the current turn's content instead.
					appendKiroText(uim, kiroToolResultText(rm))
				}
				if len(kept) == 0 {
					delete(ctx, "toolResults")
				} else {
					ctx["toolResults"] = kept
				}
				if len(ctx) == 0 {
					delete(uim, "userInputMessageContext")
				}
			}
		}
	}

	return outHistory, current
}

// reconcileKiroToolPair matches assistant toolUses against the following
// user turn's toolResults. Unmatched toolUses are flattened into the
// assistant's content as text; unmatched toolResults are flattened into
// the user's content as text. This mirrors the JS reconcileToolPair.
func reconcileKiroToolPair(arm map[string]any, nextUser map[string]any, specNames map[string]bool, usedIds map[string]bool) {
	toolUses, _ := arm["toolUses"].([]any)
	if len(toolUses) == 0 {
		return
	}

	// Collect available toolResult IDs from the next user turn.
	var results []any
	var resultIDs map[string]bool
	if nextUser != nil {
		if ctx, ok := nextUser["userInputMessageContext"].(map[string]any); ok {
			results, _ = ctx["toolResults"].([]any)
		}
	}
	resultIDs = make(map[string]bool, len(results))
	for _, r := range results {
		if rm, ok := r.(map[string]any); ok {
			if id, _ := rm["toolUseId"].(string); id != "" {
				resultIDs[id] = true
			}
		}
	}

	// Partition toolUses into matched (has result) and unmatched.
	// When specNames is empty (client sent no tools array), we do not filter
	// by spec — the tool was presumably declared in an earlier turn.
	var matched []any
	var unmatched []any
	for _, u := range toolUses {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		id, _ := um["toolUseId"].(string)
		name, _ := um["name"].(string)
		if id == "" || usedIds[id] || !resultIDs[id] {
			unmatched = append(unmatched, um)
			continue
		}
		if len(specNames) > 0 && !specNames[name] {
			unmatched = append(unmatched, um)
			continue
		}
		matched = append(matched, um)
		usedIds[id] = true
	}

	// Flatten unmatched toolUses into assistant text.
	for _, u := range unmatched {
		um, _ := u.(map[string]any)
		name, _ := um["name"].(string)
		input := um["input"]
		inputStr := ""
		if input != nil {
			if m, ok := input.(map[string]any); ok {
				inputStr = fmt.Sprintf("%v", m)
			} else if s, ok := input.(string); ok {
				inputStr = s
			}
		}
		appendKiroText(arm, fmt.Sprintf("[Tool call: %s(%s)]", name, inputStr))
	}

	if len(matched) > 0 {
		arm["toolUses"] = matched
	} else {
		delete(arm, "toolUses")
	}

	// Flatten unmatched toolResults into the next user turn's text.
	if nextUser != nil {
		var keptResults []any
		for _, r := range results {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id, _ := rm["toolUseId"].(string)
			if id == "" || !usedIds[id] {
				appendKiroText(nextUser, kiroToolResultText(rm))
				continue
			}
			keptResults = append(keptResults, rm)
		}
		if ctx, ok := nextUser["userInputMessageContext"].(map[string]any); ok {
			if len(keptResults) > 0 {
				ctx["toolResults"] = keptResults
			} else {
				delete(ctx, "toolResults")
			}
			if len(ctx) == 0 {
				delete(nextUser, "userInputMessageContext")
			}
		}
	}
}

func appendKiroText(msg map[string]any, extra string) {
	if extra == "" {
		return
	}
	existing, _ := msg["content"].(string)
	if existing == "" {
		msg["content"] = extra
	} else {
		msg["content"] = existing + "\n\n" + extra
	}
}

func kiroToolResultText(rm map[string]any) string {
	status, _ := rm["status"].(string)
	content, _ := rm["content"].([]any)
	var text string
	for _, c := range content {
		if cm, ok := c.(map[string]any); ok {
			if t, _ := cm["text"].(string); t != "" {
				if text != "" {
					text += "\n"
				}
				text += t
			}
		}
	}
	if status == "error" {
		return "[Tool result (error): " + text + "]"
	}
	return "[Tool result: " + text + "]"
}
