package executor

import (
	json "encoding/json/v2"
	"strings"
)

// codexJSONObjectHint is prepended to input when text.format is json_object
// but no input message mentions JSON. The ChatGPT Codex backend answers 502
// "Response input messages must contain the word 'json' in some form to use
// 'text.format' of type 'json_object'" otherwise. Instructions do not count:
// buildResponsesBody moves system/developer text into top-level instructions,
// which the validator excludes from "input messages".
const codexJSONObjectHint = "Respond with a JSON object."

// injectCodexJSONObjectHint ensures the Codex json_object contract holds: when
// text.format.type is json_object and no input item carries the word "json"
// (case-insensitive) in any of its text fields, a developer message with the
// hint is prepended to input. json_schema needs no hint, and a body without
// structured output is returned unchanged.
func injectCodexJSONObjectHint(body []byte) []byte {
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	format, ok := envelope["text"].(map[string]any)
	if !ok {
		return body
	}
	if t, _ := format["format"].(map[string]any); t != nil {
		if typ, _ := t["type"].(string); typ != "json_object" {
			return body
		}
	} else if ft, _ := format["format"].(string); ft != "json_object" {
		return body
	}

	input, ok := responseInputItems(envelope["input"])
	if !ok {
		return body
	}
	for _, item := range input {
		if itemContainsJSONWord(item) {
			return body
		}
	}

	envelope["input"] = append([]any{codexJSONObjectHintMessage()}, input...)
	out, err := marshalStable(envelope)
	if err != nil {
		return body
	}
	return out
}

func codexJSONObjectHintMessage() map[string]any {
	return map[string]any{
		"type":    "message",
		"role":    "developer",
		"content": []any{map[string]any{"type": "input_text", "text": codexJSONObjectHint}},
	}
}

// itemContainsJSONWord reports whether an input item (or any nested map or
// list inside it) carries a string value containing "json" case-insensitively.
// It is deliberately broad: text fields, tool arguments and outputs all count
// as "some form" for the backend's validator.
func itemContainsJSONWord(node any) bool {
	switch v := node.(type) {
	case string:
		return strings.Contains(strings.ToLower(v), "json")
	case []any:
		for _, child := range v {
			if itemContainsJSONWord(child) {
				return true
			}
		}
	case map[string]any:
		for _, child := range v {
			if itemContainsJSONWord(child) {
				return true
			}
		}
	}
	return false
}
