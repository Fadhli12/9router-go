package translator

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

func decodeKiro(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal kiro envelope: %v", err)
	}
	return m
}

func TestOpenAIToKiro_BuildsConversationState(t *testing.T) {
	body := []byte(`{"model":"kr/claude-sonnet-4.5","messages":[{"role":"user","content":"hi"}],"max_tokens":1024,"stream":false}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "claude-sonnet-4.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	m := decodeKiro(t, out)
	if _, ok := m["systemPrompt"]; ok {
		t.Error("top-level systemPrompt must not be present (gateway 400s on it)")
	}
	cs, ok := m["conversationState"].(map[string]any)
	if !ok {
		t.Fatalf("missing conversationState, got keys %v", m)
	}
	if cs["chatTriggerType"] != "MANUAL" {
		t.Errorf("chatTriggerType = %v, want MANUAL", cs["chatTriggerType"])
	}
	if _, ok := cs["conversationId"].(string); !ok {
		t.Error("missing conversationId")
	}
	cur, ok := cs["currentMessage"].(map[string]any)
	if !ok {
		t.Fatal("missing currentMessage")
	}
	uim, ok := cur["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatal("missing currentMessage.userInputMessage")
	}
	if uim["modelId"] != "claude-sonnet-4.5" {
		t.Errorf("modelId = %v, want claude-sonnet-4.5", uim["modelId"])
	}
	if uim["origin"] != "AI_EDITOR" {
		t.Errorf("origin = %v, want AI_EDITOR", uim["origin"])
	}
	content, _ := uim["content"].(string)
	if !strings.Contains(content, "hi") {
		t.Errorf("current content should carry the user text, got %q", content)
	}
	// system/time prefix must be inside the user turn, not top-level.
	if !strings.Contains(content, "[Context: Current time is") {
		t.Errorf("expected time context inside user turn, got %q", content)
	}
	ic, ok := m["inferenceConfig"].(map[string]any)
	if !ok {
		t.Fatal("missing inferenceConfig")
	}
	mt, _ := ic["maxTokens"].(float64)
	if mt != kiroDefaultMaxTokens {
		t.Errorf("maxTokens = %v, want %d", ic["maxTokens"], kiroDefaultMaxTokens)
	}
}

func TestOpenAIToKiro_SystemBecomesInstructionsInUserTurn(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","content":"You are terse."},{"role":"user","content":"hello"}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "claude-sonnet-4.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	uim := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	content, _ := uim["content"].(string)
	if !strings.Contains(content, "<instructions>") || !strings.Contains(content, "You are terse.") {
		t.Errorf("system prompt must be wrapped in <instructions> inside the user turn, got %q", content)
	}
}

func TestOpenAIToKiro_MultiTurnHistoryAlternates(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"system","content":"sys"},
		{"role":"user","content":"first"},
		{"role":"assistant","content":"answer1"},
		{"role":"user","content":"second"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)
	if len(hist) == 0 {
		t.Fatal("expected history turns")
	}
	// The trailing user turn is the current message; history holds earlier turns.
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	if c, _ := cur["content"].(string); !strings.Contains(c, "second") {
		t.Errorf("current turn should be the last user message, got %q", c)
	}
	// No two adjacent user turns (Kiro requires alternation).
	prevUser := false
	for _, h := range hist {
		turn := h.(map[string]any)
		_, isUser := turn["userInputMessage"]
		if isUser && prevUser {
			t.Error("history has consecutive user turns")
		}
		prevUser = isUser
		if isUser {
			uim := turn["userInputMessage"].(map[string]any)
			if uim["modelId"] != "m" {
				t.Errorf("history user turn modelId = %v, want m", uim["modelId"])
			}
		}
	}
}

func TestOpenAIToKiro_ToolCallsAndResults(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"sunny"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)
	found := false
	for _, h := range hist {
		arm, ok := h.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		uses, _ := arm["toolUses"].([]any)
		if len(uses) == 0 {
			continue
		}
		found = true
		u := uses[0].(map[string]any)
		if u["name"] != "get_weather" || u["toolUseId"] != "call_1" {
			t.Errorf("unexpected toolUse: %v", u)
		}
		input := u["input"].(map[string]any)
		if input["city"] != "NYC" {
			t.Errorf("tool input not parsed from arguments JSON: %v", input)
		}
	}
	if !found {
		t.Error("expected an assistant turn carrying toolUses in history")
	}
	// Current (tool result) user turn carries userInputMessageContext.toolResults.
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	ctx, _ := cur["userInputMessageContext"].(map[string]any)
	results, _ := ctx["toolResults"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result in current turn, got %v", ctx)
	}
	if results[0].(map[string]any)["toolUseId"] != "call_1" {
		t.Errorf("unexpected tool result: %v", results[0])
	}
}

func TestOpenAIToKiro_ImageBecomesKiroBlock(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"text","text":"what is this?"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}
	]}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	uim := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	imgs, _ := uim["images"].([]any)
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image, got %v", uim["images"])
	}
	img := imgs[0].(map[string]any)
	if img["format"] != "png" {
		t.Errorf("format = %v, want png", img["format"])
	}
	src := img["source"].(map[string]any)
	if src["bytes"] != "AAAA" {
		t.Errorf("source.bytes = %v, want AAAA", src["bytes"])
	}
}

func TestOpenAIToKiro_ProfileArnOnlyWhenProvided(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m", ProfileArn: "arn:aws:codewhisperer:us-east-1:1:profile/p"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decodeKiro(t, out)["profileArn"] != "arn:aws:codewhisperer:us-east-1:1:profile/p" {
		t.Error("profileArn should be forwarded when provided")
	}

	out2, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := decodeKiro(t, out2)["profileArn"]; ok {
		t.Error("profileArn must be omitted when empty (shared placeholder belongs to another account)")
	}
}

// TestOpenAIToKiro_OrphanToolResultBecomesText covers Kiro's
// "Bad Request: Invalid tool use format." — a tool result whose
// tool_call_id does not match any toolUseId emitted by the preceding
// assistant turn must never reach userInputMessageContext.toolResults.
func TestOpenAIToKiro_OrphanToolResultBecomesText(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},
		{"role":"tool","tool_call_id":"call_999_stale","content":"sunny"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)

	if ctx, ok := cur["userInputMessageContext"].(map[string]any); ok {
		if results, _ := ctx["toolResults"].([]any); len(results) > 0 {
			t.Fatalf("orphan tool result must not reach toolResults, got %v", results)
		}
	}
	content, _ := cur["content"].(string)
	if !strings.Contains(content, "sunny") {
		t.Errorf("orphan tool result content should be folded into text, got %q", content)
	}
}

// TestOpenAIToKiro_ToolMessageWithoutIDBecomesText covers rule 3: a tool-role
// message with no tool_call_id at all must be sanitized to plain text rather
// than silently dropped or passed through with an empty toolUseId.
func TestOpenAIToKiro_ToolMessageWithoutIDBecomesText(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
		{"role":"tool","content":"sunny, no id attached"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)

	if ctx, ok := cur["userInputMessageContext"].(map[string]any); ok {
		if results, _ := ctx["toolResults"].([]any); len(results) > 0 {
			t.Fatalf("id-less tool message must not reach toolResults, got %v", results)
		}
	}
	content, _ := cur["content"].(string)
	if !strings.Contains(content, "sunny, no id attached") {
		t.Errorf("id-less tool message content should be folded into text, got %q", content)
	}
}

// TestOpenAIToKiro_ToolResultBlockOrphanBecomesText covers the Claude-shape
// tool_result content block (not the OpenAI tool_call_id envelope) with an
// id that was never emitted by the preceding assistant turn.
func TestOpenAIToKiro_ToolResultBlockOrphanBecomesText(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"orphan_id","content":"cloudy"}]}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)

	if ctx, ok := cur["userInputMessageContext"].(map[string]any); ok {
		if results, _ := ctx["toolResults"].([]any); len(results) > 0 {
			t.Fatalf("orphan tool_result block must not reach toolResults, got %v", results)
		}
	}
	content, _ := cur["content"].(string)
	if !strings.Contains(content, "cloudy") {
		t.Errorf("orphan tool_result block content should be folded into text, got %q", content)
	}
}

func TestOpenAIToKiro_EmptyUserTurnGetsPlaceholder(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":""}]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	uim := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	c, _ := uim["content"].(string)
	if !strings.Contains(c, kiroEmptyUserPlaceholder) {
		t.Errorf("empty user turn should get %q placeholder, got %q", kiroEmptyUserPlaceholder, c)
	}
}

// kiroHistoryAssistantTexts collects the content string of every
// assistantResponseMessage turn in history, in order.
func kiroHistoryAssistantTexts(hist []any) []string {
	var out []string
	for _, h := range hist {
		turn, ok := h.(map[string]any)
		if !ok {
			continue
		}
		arm, ok := turn["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		c, _ := arm["content"].(string)
		out = append(out, c)
	}
	return out
}

// TestOpenAIToKiro_SanitizesOrphanToolsAndEmptyAssistantShells covers the
// exact failure shape SanitizeOpenAIMessages exists for: alternating tool
// messages carrying no tool_call_id at all, interleaved with empty
// assistant shells that otherwise precede them. Before sanitization was
// wired in, kiroConvertMessages would both (a) emit a dummy "..."
// assistantResponseMessage for every empty shell and (b) try to place the
// id-less tool content as a toolResult, which Kiro rejects with "Invalid
// tool use format." The sanitized conversation must build a valid
// conversationState with no "..." placeholder turn and no toolResults
// entries, and must preserve the tool content as readable text instead of
// dropping it.
func TestOpenAIToKiro_SanitizesOrphanToolsAndEmptyAssistantShells(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"run the two checks"},
		{"role":"assistant","content":""},
		{"role":"tool","content":"first check: pass"},
		{"role":"assistant","content":""},
		{"role":"tool","content":"second check: pass"},
		{"role":"assistant","content":"Both checks passed."},
		{"role":"user","content":"thanks, run one more"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)

	for _, text := range kiroHistoryAssistantTexts(hist) {
		if text == kiroAssistantPlaceholder {
			t.Errorf("history must not contain a dummy %q assistant turn after sanitization, got turns %v", kiroAssistantPlaceholder, kiroHistoryAssistantTexts(hist))
		}
	}

	// No two adjacent user turns (Kiro requires strict alternation), and no
	// turn may carry a toolResults entry since every tool message here was
	// id-less and must have been folded into user text instead.
	prevUser := false
	for _, h := range hist {
		turn := h.(map[string]any)
		uim, isUser := turn["userInputMessage"].(map[string]any)
		if isUser && prevUser {
			t.Error("history has consecutive user turns")
		}
		prevUser = isUser
		if !isUser {
			prevUser = false
			continue
		}
		if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
			if results, _ := ctx["toolResults"].([]any); len(results) > 0 {
				t.Errorf("id-less tool message must not reach toolResults, got %v", results)
			}
		}
	}

	// The sanitized tool content must still be present as readable text
	// somewhere in the conversation (not silently dropped).
	full := kiroFlattenConversationText(t, m)
	for _, want := range []string{"first check: pass", "second check: pass"} {
		if !strings.Contains(full, want) {
			t.Errorf("expected sanitized tool content %q to survive as text, got %q", want, full)
		}
	}
}

// kiroFlattenConversationText concatenates every user/assistant content
// string across history and the current message, for substring assertions
// that don't care which turn ended up holding the text.
func kiroFlattenConversationText(t *testing.T, m map[string]any) string {
	t.Helper()
	var b strings.Builder
	cs, ok := m["conversationState"].(map[string]any)
	if !ok {
		return ""
	}
	hist, _ := cs["history"].([]any)
	for _, h := range hist {
		turn, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if uim, ok := turn["userInputMessage"].(map[string]any); ok {
			if c, _ := uim["content"].(string); c != "" {
				b.WriteString(c)
				b.WriteString("\n")
			}
		}
		if arm, ok := turn["assistantResponseMessage"].(map[string]any); ok {
			if c, _ := arm["content"].(string); c != "" {
				b.WriteString(c)
				b.WriteString("\n")
			}
		}
	}
	if cur, ok := cs["currentMessage"].(map[string]any); ok {
		if uim, ok := cur["userInputMessage"].(map[string]any); ok {
			if c, _ := uim["content"].(string); c != "" {
				b.WriteString(c)
			}
		}
	}
	return b.String()
}

// TestOpenAIToKiro_EmptyAssistantShellBeforeToolCallsIsDropped covers the
// legitimate (non-orphan) shell shape: an assistant turn with no text that
// immediately precedes its own tool_calls announcement is NOT what the
// sanitizer targets (SanitizeOpenAIMessages only drops a *separate*
// contentless assistant message that precedes a *tool*-role message), so it
// must still flow through to toolUses as before. This guards against a
// regression where sanitization accidentally eats legitimate tool_calls.
func TestOpenAIToKiro_EmptyAssistantShellBeforeToolCallsIsDropped(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"sunny"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)
	found := false
	for _, h := range hist {
		arm, ok := h.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		if uses, _ := arm["toolUses"].([]any); len(uses) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("legitimate tool_calls announcement must survive sanitization and reach history as toolUses")
	}
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	ctx, _ := cur["userInputMessageContext"].(map[string]any)
	results, _ := ctx["toolResults"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["toolUseId"] != "call_1" {
		t.Errorf("matched tool result must still reach toolResults, got %v", ctx)
	}
}

// kiroHasDummyShell reports whether any assistantResponseMessage turn in
// history is a placeholder with NO toolUses — i.e. a genuinely empty assistant
// turn. Kiro rejects those with "Invalid tool use format". A turn that carries
// toolUses legitimately has no text (the "..." placeholder fills its content),
// so it is not a dummy shell.
func kiroHasDummyShell(hist []any) bool {
	for _, h := range hist {
		arm, ok := h.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		if c, _ := arm["content"].(string); c != kiroAssistantPlaceholder {
			continue
		}
		if uses, _ := arm["toolUses"].([]any); len(uses) > 0 {
			continue
		}
		return true
	}
	return false
}

// TestOpenAIToKiro_StripsThinkingBlocks covers the OpenCode/Sisyphus shape: an
// assistant turn whose content array carries a "thinking" block followed by the
// model's real answer. The thinking block must be dropped and the real answer
// must survive — with no dummy "..." shell.
func TestOpenAIToKiro_StripsThinkingBlocks(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"hello"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"pondering"},{"type":"text","text":"hi there"}]},
		{"role":"user","content":"go on"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)

	texts := kiroHistoryAssistantTexts(hist)
	if len(texts) == 0 {
		t.Fatalf("expected an assistant turn in history, got none")
	}
	for _, text := range texts {
		if strings.Contains(text, "pondering") {
			t.Errorf("thinking content must be stripped, got %q", text)
		}
	}
	if texts[len(texts)-1] != "hi there" {
		t.Errorf("real assistant text must survive thinking stripping, got %v", texts)
	}
	if kiroHasDummyShell(hist) {
		t.Errorf("history must not contain a dummy %q assistant turn, got turns %v", kiroAssistantPlaceholder, texts)
	}
}

// TestOpenAIToKiro_StripsReasoningContentField covers the OpenAI
// `reasoning_content` string field. A turn with real text plus reasoning must
// keep only the text, and a reasoning-ONLY turn must be dropped entirely (no
// dummy "..." shell).
func TestOpenAIToKiro_StripsReasoningContentField(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"hello"},
		{"role":"assistant","content":"","reasoning_content":"hidden chain of thought"},
		{"role":"user","content":"go on"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)

	for _, text := range kiroHistoryAssistantTexts(hist) {
		if strings.Contains(text, "hidden chain of thought") {
			t.Errorf("reasoning_content must be stripped, got %q", text)
		}
	}
	if kiroHasDummyShell(hist) {
		t.Errorf("reasoning-only assistant turn must be dropped, not left as a dummy %q shell", kiroAssistantPlaceholder)
	}
}

// TestOpenAIToKiro_ThinkingOnlyTurnBeforeToolCallStillEmitsToolUses guards the
// agentic shape: an assistant turn that carries thinking AND tool_calls. The
// thinking is stripped, but the tool_calls must still surface as toolUses and
// pair with the following tool result — the exact multi-turn sequence
// Sisyphus/OpenCode send and where a regression would 400 upstream.
func TestOpenAIToKiro_ThinkingOnlyTurnBeforeToolCallStillEmitsToolUses(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"should call the tool"}],"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"sunny"}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)

	found := false
	for _, h := range hist {
		arm, ok := h.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		if uses, _ := arm["toolUses"].([]any); len(uses) > 0 {
			found = true
			u := uses[0].(map[string]any)
			if u["toolUseId"] != "call_1" || u["name"] != "get_weather" {
				t.Errorf("unexpected toolUse: %v", u)
			}
		}
	}
	if !found {
		t.Error("tool_calls must survive thinking stripping and reach history as toolUses")
	}
	if kiroHasDummyShell(hist) {
		t.Errorf("history must not contain a dummy %q assistant turn", kiroAssistantPlaceholder)
	}

	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	ctx, _ := cur["userInputMessageContext"].(map[string]any)
	results, _ := ctx["toolResults"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["toolUseId"] != "call_1" {
		t.Errorf("matched tool result must still reach toolResults, got %v", ctx)
	}
}

// TestOpenAIToKiro_CanonicalizesToolCallName covers the Sisyphus shape where a
// tool name contains characters Kiro's tool-name sanitizer rewrites (dots →
// underscores). The tool-call name must be canonicalized to the same sanitized
// name the spec carries, or CodeWhisperer rejects the turn with "Invalid tool
// use format.".
func TestOpenAIToKiro_CanonicalizesToolCallName(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"do it"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"mcp.server.tool","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"done"}
	],"tools":[
		{"type":"function","function":{"name":"mcp.server.tool","description":"a tool","parameters":{"type":"object","properties":{}}}}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"name":"mcp_server_tool"`) {
		t.Errorf("tool call name must be canonicalized to mcp_server_tool:\n%s", s)
	}
	if strings.Contains(s, `"name":"mcp.server.tool"`) {
		t.Errorf("raw un-canonicalized tool call name leaked:\n%s", s)
	}
}

// TestOpenAIToKiro_DropsToolCallWithoutSpec covers a tool call whose name has
// no matching tool spec. It must not be emitted as a toolUse (CodeWhisperer
// rejects an undeclared tool with "Invalid tool use format."), so the assistant
// turn must carry no toolUses at all.
func TestOpenAIToKiro_DropsToolCallWithoutSpec(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"do it"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"ghost_tool","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"done"}
	],"tools":[
		{"type":"function","function":{"name":"known_tool","description":"a tool","parameters":{"type":"object","properties":{}}}}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	s := string(out)
	if strings.Contains(s, `"name":"ghost_tool"`) {
		t.Errorf("tool call with no matching spec must be dropped:\n%s", s)
	}
}

// TestOpenAIToKiro_MissingToolResultFlattensToolUse covers the case where an
// assistant emitted two parallel tool calls but only one returned a result
// (the other was cancelled/failed). Kiro rejects the unmatched toolUse with
// "Invalid tool use format.", so it must be flattened into text instead.
func TestOpenAIToKiro_MissingToolResultFlattensToolUse(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"do A and B"},
		{"role":"assistant","content":"","tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"grep","arguments":"{}"}},
			{"id":"call_2","type":"function","function":{"name":"read","arguments":"{}"}}
		]},
		{"role":"tool","tool_call_id":"call_1","content":"A done"},
		{"role":"assistant","content":"Done."},
		{"role":"user","content":"next"}
	],"tools":[
		{"type":"function","function":{"name":"grep","description":"search","parameters":{"type":"object","properties":{}}}},
		{"type":"function","function":{"name":"read","description":"read","parameters":{"type":"object","properties":{}}}}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	hist, _ := cs["history"].([]any)

	// The assistant turn must NOT carry call_2 as a toolUse — it has no result.
	for _, h := range hist {
		arm, ok := h.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		if uses, _ := arm["toolUses"].([]any); len(uses) > 0 {
			for _, u := range uses {
				um := u.(map[string]any)
				if um["toolUseId"] == "call_2" {
					t.Errorf("unmatched toolUse call_2 must be flattened, got %v", arm)
				}
			}
		}
	}
	// call_2 must appear as flattened text instead.
	foundText := false
	for _, h := range hist {
		arm, ok := h.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		if c, _ := arm["content"].(string); strings.Contains(c, "[Tool call: read") {
			foundText = true
		}
	}
	if !foundText {
		t.Errorf("unmatched toolUse call_2 must appear as flattened text")
	}
	// call_1 must still be a proper toolUse with its result.
	foundPair := false
	for i, h := range hist {
		arm, ok := h.(map[string]any)["assistantResponseMessage"].(map[string]any)
		if !ok {
			continue
		}
		if uses, _ := arm["toolUses"].([]any); len(uses) > 0 {
			for _, u := range uses {
				um := u.(map[string]any)
				if um["toolUseId"] == "call_1" {
					// Next turn must be a user turn with the matching result.
					if i+1 < len(hist) {
						uim, _ := hist[i+1].(map[string]any)["userInputMessage"].(map[string]any)
						ctx, _ := uim["userInputMessageContext"].(map[string]any)
						results, _ := ctx["toolResults"].([]any)
						for _, r := range results {
							rm := r.(map[string]any)
							if rm["toolUseId"] == "call_1" {
								foundPair = true
							}
						}
					}
				}
			}
		}
	}
	if !foundPair {
		t.Errorf("matched toolUse call_1 must still pair with its result")
	}
}

// TestOpenAIToKiro_OrphanToolResultInCurrentTurnIsFlattened covers the case
// where the current user turn carries a tool result that no assistant turn
// ever emitted — it must be flattened into text instead of reaching
// userInputMessageContext.toolResults.
func TestOpenAIToKiro_OrphanToolResultInCurrentTurnIsFlattened(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":"do A"},
		{"role":"assistant","content":"Done."},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"orphan_id","content":"mystery result"},
			{"type":"text","text":"next"}
		]}
	],"tools":[
		{"type":"function","function":{"name":"grep","description":"search","parameters":{"type":"object","properties":{}}}}
	]}`)
	out, err := OpenAIToKiro(body, KiroTranslateOptions{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := decodeKiro(t, out)
	cs := m["conversationState"].(map[string]any)
	cur := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	ctx, _ := cur["userInputMessageContext"].(map[string]any)
	if results, _ := ctx["toolResults"].([]any); len(results) > 0 {
		t.Errorf("orphan tool result in current turn must not reach toolResults, got %v", results)
	}
	content, _ := cur["content"].(string)
	if !strings.Contains(content, "mystery result") {
		t.Errorf("orphan tool result content must be folded into current turn text, got %q", content)
	}
}
