package executor

import (
	json "encoding/json/v2"
	"testing"
)

func decodeJSONObjectBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return m
}

func inputItems(t *testing.T, m map[string]any) []any {
	t.Helper()
	items, ok := m["input"].([]any)
	if !ok {
		t.Fatalf("input is not a list: %T", m["input"])
	}
	return items
}

func firstItemText(t *testing.T, item any) string {
	t.Helper()
	im, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("item not a map: %T", item)
	}
	if im["role"] != "developer" && im["role"] != "system" {
		return ""
	}
	content, ok := im["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	cm, ok := content[0].(map[string]any)
	if !ok {
		return ""
	}
	txt, _ := cm["text"].(string)
	return txt
}

// The bug: response_format json_object with user text lacking "json" maps to
// text.format json_object, but the only JSON mention can live in instructions
// (system/developer text is hoisted there), so the Codex backend 502s with
// "Response input messages must contain the word 'json' in some form".
func TestInjectCodexJSONObjectHint(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantHint   bool
		wantPrefix string
	}{
		{
			name: "json_object with no json word in input gets developer hint",
			body: `{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"How many people live in Jakarta?"}]}],"instructions":"You are helpful.","stream":true,"store":false,"text":{"format":{"type":"json_object"}}}`,
			wantHint:   true,
			wantPrefix: codexJSONObjectHint,
		},
		{
			name: "json_object with json word in user text is untouched",
			body: `{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Return a JSON object with 2 people."}]}],"stream":true,"store":false,"text":{"format":{"type":"json_object"}}}`,
			wantHint: false,
		},
		{
			name: "json_schema format is untouched",
			body: `{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"How many people?"}]}],"stream":true,"store":false,"text":{"format":{"type":"json_schema","name":"r","strict":true,"schema":{"type":"object"}}}}`,
			wantHint: false,
		},
		{
			name:     "no text field is untouched",
			body:     `{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]}],"stream":true,"store":false}`,
			wantHint: false,
		},
		{
			name: "json word inside tool output counts",
			body: `{"model":"gpt-6-astra","input":[{"type":"function_call","call_id":"call_0","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"call_0","output":"{\"json\":true}"}],"stream":true,"store":false,"text":{"format":{"type":"json_object"}}}`,
			wantHint: false,
		},
		{
			name: "json word case-insensitive in input counts",
			body: `{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"give me Json output"}]}],"stream":true,"store":false,"text":{"format":{"type":"json_object"}}}`,
			wantHint: false,
		},
		{
			name: "empty input list with json_object gets hint",
			body: `{"model":"gpt-6-astra","input":[],"stream":true,"store":false,"text":{"format":{"type":"json_object"}}}`,
			wantHint:   true,
			wantPrefix: codexJSONObjectHint,
		},
		{
			name: "string-only json word in instructions does not count",
			body: `{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"List planets."}]}],"instructions":"Always emit json.","stream":true,"store":false,"text":{"format":{"type":"json_object"}}}`,
			wantHint:   true,
			wantPrefix: codexJSONObjectHint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := injectCodexJSONObjectHint([]byte(tt.body))
			m := decodeJSONObjectBody(t, out)
			items := inputItems(t, m)
			if !tt.wantHint {
				for _, item := range items {
					if firstItemText(t, item) == codexJSONObjectHint {
						t.Fatalf("hint injected but should not be; items=%v", items)
					}
				}
				// Body must be equivalent to input modulo no prefix.
				if len(items) == 0 {
					t.Fatalf("input emptied")
				}
				return
			}
			if len(items) == 0 {
				t.Fatalf("no input items after injection")
			}
			got := firstItemText(t, items[0])
			if got != tt.wantPrefix {
				t.Fatalf("first item text = %q, want %q", got, tt.wantPrefix)
			}
		})
	}
}

// The Chat Completions → Responses pipeline must end with the hint: this is
// the end-to-end shape ForwardCodex sends after buildResponsesBody.
func TestInjectCodexJSONObjectHintAfterBuildResponsesBody(t *testing.T) {
	chat := `{"model":"gpt-6-astra","messages":[
		{"role":"system","content":"Answer briefly."},
		{"role":"user","content":"2 people live in Jakarta. How many?"}
	],"response_format":{"type":"json_object"}}`
	responses, _, err := buildResponsesBody([]byte(chat))
	if err != nil {
		t.Fatalf("buildResponsesBody: %v", err)
	}
	out := injectCodexJSONObjectHint(responses)
	m := decodeJSONObjectBody(t, out)
	items := inputItems(t, m)
	if len(items) < 2 {
		t.Fatalf("expected injected prefix + user item, got %d items", len(items))
	}
	if firstItemText(t, items[0]) != codexJSONObjectHint {
		t.Fatalf("first item not the hint: %v", items[0])
	}
	// text.format mapping must be preserved (#2896 contract).
	if tf, ok := m["text"].(map[string]any); !ok || tf["format"].(map[string]any)["type"] != "json_object" {
		t.Fatalf("text.format json_object mapping lost: %v", m["text"])
	}
}

// End-to-end through the simulated validator: the exact runtime rejection
// reproduced offline. The upstream fake enforces the same rule the ChatGPT
// Codex backend does — json_object requires "json" in input messages.
func TestCodexJSONObjectHintSatisfiesValidator(t *testing.T) {
	validate := func(body []byte) string {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		if tf, ok := m["text"].(map[string]any); ok {
			if f, ok := tf["format"].(map[string]any); ok && f["type"] == "json_object" {
				if !itemContainsJSONWord(m["input"]) {
					return "Response input messages must contain the word 'json' in some form to use 'text.format' of type 'json_object'."
				}
			}
		}
		return ""
	}

	chat := `{"model":"gpt-6-astra","messages":[
		{"role":"system","content":"Answer briefly."},
		{"role":"user","content":"2 people live in Jakarta. How many?"}
	],"response_format":{"type":"json_object"}}`
	responses, _, err := buildResponsesBody([]byte(chat))
	if err != nil {
		t.Fatalf("buildResponsesBody: %v", err)
	}
	if msg := validate(responses); msg == "" {
		t.Fatalf("validator accepted the un-fixed body; test does not reproduce the bug")
	}
	fixed := injectCodexJSONObjectHint(responses)
	if msg := validate(fixed); msg != "" {
		t.Fatalf("validator rejected the fixed body: %s", msg)
	}
}
